/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package feastoperator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

const (
	capabilitiesConfigMapName = "feast-capabilities-config"

	capabilitiesKeyFeatureStoreEnabled = "featureStoreEnabled"
	capabilitiesKeyDataRegistryEnabled = "dataRegistryEnabled"
	capabilitiesKeyDataRegistryNS      = "dataRegistryNamespace"

	capabilitiesHashAnnotation = "feast.dev/capabilities-hash"
)

// CapabilitiesConfigMapFinalizer prevents deletion until the controller can
// release the ConfigMap and recreate it through normal reconciliation.
const CapabilitiesConfigMapFinalizer = "components.platform.opendatahub.io/feast-capabilities"

// feastOperatorCapabilitiesFinalizer is added by the feast operator to the
// capabilities ConfigMap. Its absence on a Terminating ConfigMap signals that
// the feast operator finished cleaning up its resources (Data Registry
// teardown) and the feast operator deployment can be removed. This is part of
// the cross-repo contract with the feast operator.
const feastOperatorCapabilitiesFinalizer = "feast.dev/capabilities-config-cleanup"

// feastOperatorReleaseTimeout bounds how long the module finalizer waits for
// the feast operator to remove its ConfigMap finalizer before proceeding
// with uninstall anyway, leaving the Terminating ConfigMap to manual cleanup.
const feastOperatorReleaseTimeout = 5 * time.Minute

// capabilitiesCleanupStartedAnnotation is the RFC3339 time at which teardown
// intent was first written. The uninstall wait is measured from this instant,
// not from the ConfigMap deletionTimestamp.
const capabilitiesCleanupStartedAnnotation = "components.platform.opendatahub.io/capabilities-cleanup-started-at"

// capabilitiesCleanupTimeoutReason is the Warning event emitted when uninstall
// continues while feast.dev/capabilities-config-cleanup still holds the ConfigMap.
const capabilitiesCleanupTimeoutReason = "CapabilitiesCleanupTimeout"

func boolString(value bool) string {
	return strconv.FormatBool(value)
}

// reconcileCapabilitiesConfigMap creates/updates the feast-capabilities-config ConfigMap
// consumed by the upstream feast-operator at startup.
// The upstream operator reads this ConfigMap to determine which reconciliation branches to activate
// (featureStoreEnabled / dataRegistryEnabled / dataRegistryNamespace).
//
// Resolution order:
//  1. If spec.capabilities is present on the FeastOperator CR, use those values (authoritative).
//  2. Otherwise fall back to process-level environment defaults (backward compat).
//
// The data registry namespace is always rhoai-data-registry (hardcoded).
func (m *Module) reconcileCapabilitiesConfigMap(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)

	feast, ok := rr.Instance.(*componentApi.FeastOperator)
	if !ok {
		return errors.New("instance is not a FeastOperator")
	}

	fsEnabled, drEnabled := m.resolveCapabilities(feast)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: m.cfg.ApplicationsNamespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, rr.Client, cm, func() error {
		if cm.DeletionTimestamp != nil {
			// Let an explicit deletion finish. The owned-resource delete event
			// schedules another reconcile, which recreates the ConfigMap.
			controllerutil.RemoveFinalizer(cm, CapabilitiesConfigMapFinalizer)
			return nil
		}
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels[labels.ODH.Component(componentName)] = labels.True
		controllerutil.AddFinalizer(cm, CapabilitiesConfigMapFinalizer)
		cm.Data = map[string]string{
			capabilitiesKeyFeatureStoreEnabled: boolString(fsEnabled),
			capabilitiesKeyDataRegistryEnabled: boolString(drEnabled),
		}
		if drEnabled {
			cm.Data[capabilitiesKeyDataRegistryNS] = dataRegistryNamespaceName
		}
		return controllerutil.SetControllerReference(feast, cm, rr.Client.Scheme())
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile capabilities ConfigMap: %w", err)
	}
	if cm.DeletionTimestamp != nil {
		log.V(1).Info("Capabilities ConfigMap deletion is in progress; waiting for the delete event to recreate it",
			"configmap", capabilitiesConfigMapName,
			"namespace", m.cfg.ApplicationsNamespace,
		)
		return nil
	}

	log.V(1).Info("Reconciled capabilities ConfigMap",
		"configmap", capabilitiesConfigMapName,
		"namespace", m.cfg.ApplicationsNamespace,
		"operation", op,
		"featureStoreEnabled", fsEnabled,
		"dataRegistryEnabled", drEnabled,
		"dataRegistryNamespace", dataRegistryNamespaceName,
	)

	return nil
}

// resolveCapabilities returns the effective capability states.
// When spec.capabilities is present on the CR it is authoritative; otherwise
// the module falls back to its process-level environment configuration.
// The data registry namespace is always rhoai-data-registry (hardcoded).
func (m *Module) resolveCapabilities(feast *componentApi.FeastOperator) (fsEnabled, drEnabled bool) {
	if feast.Spec.Capabilities != nil {
		fsEnabled = feast.Spec.Capabilities.FeatureStore.ManagementState == componentApi.CapabilityManaged
		drEnabled = feast.Spec.Capabilities.DataRegistry.ManagementState == componentApi.CapabilityManaged
	} else {
		fsEnabled = m.cfg.FeatureStoreEnabled
		drEnabled = m.cfg.DataRegistryEnabled
	}

	return fsEnabled, drEnabled
}

// triggerCapabilityRolloutIfNeeded annotates the upstream feast-operator Deployment's
// pod template with a hash of the capabilities ConfigMap data. The upstream operator
// reads the ConfigMap at startup only, so updating the ConfigMap alone is not enough;
// the annotation change triggers a rolling restart.
func (m *Module) triggerCapabilityRolloutIfNeeded(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)

	cm := &corev1.ConfigMap{}
	if err := rr.Client.Get(ctx, client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: m.cfg.ApplicationsNamespace,
	}, cm); err != nil {
		if k8serr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get capabilities ConfigMap: %w", err)
	}

	hash := capabilitiesDataHash(cm.Data)

	deploy := &appsv1.Deployment{}
	deployKey := client.ObjectKey{
		Name:      deploymentName,
		Namespace: m.cfg.ApplicationsNamespace,
	}
	if err := rr.Client.Get(ctx, deployKey, deploy); err != nil {
		if k8serr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Deployment %s for capability rollout check: %w", deploymentName, err)
	}

	currentHash := ""
	if deploy.Spec.Template.Annotations != nil {
		currentHash = deploy.Spec.Template.Annotations[capabilitiesHashAnnotation]
	}
	if currentHash == hash {
		return nil
	}

	patch := client.MergeFrom(deploy.DeepCopy())
	if deploy.Spec.Template.Annotations == nil {
		deploy.Spec.Template.Annotations = map[string]string{}
	}
	deploy.Spec.Template.Annotations[capabilitiesHashAnnotation] = hash
	if err := rr.Client.Patch(ctx, deploy, patch); err != nil {
		return fmt.Errorf("failed to patch Deployment %s with capabilities hash: %w", deploymentName, err)
	}

	log.Info("Patched Deployment pod template to trigger rollout after capability change",
		"deployment", deploymentName,
		"capabilitiesHash", hash,
	)

	return nil
}

// capabilitiesDataHash returns a short deterministic hash of ConfigMap data
// suitable for use as a pod template annotation.
func capabilitiesDataHash(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte("="))
		h.Write([]byte(data[k]))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// getCapabilitiesConfigMap reads the capabilities ConfigMap. Not-found is not
// an error; callers treat a missing ConfigMap as nothing to hand over.
func (m *Module) getCapabilitiesConfigMap(ctx context.Context, rr *odhtypes.ReconciliationRequest) (*corev1.ConfigMap, error) {
	cm := &corev1.ConfigMap{}
	if err := rr.Client.Get(ctx, client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: m.cfg.ApplicationsNamespace,
	}, cm); err != nil {
		if k8serr.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get capabilities ConfigMap %s: %w", capabilitiesConfigMapName, err)
	}
	return cm, nil
}

// markCapabilitiesDisabled writes both capability flags as disabled into the
// capabilities ConfigMap, including one that is already terminating. The feast
// operator treats dataRegistryEnabled=false on a Terminating ConfigMap as
// teardown intent. The same update records capabilitiesCleanupStartedAnnotation
// once. The write is skipped when both flags are already disabled and that
// annotation is set.
func (m *Module) markCapabilitiesDisabled(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	cm, err := m.getCapabilitiesConfigMap(ctx, rr)
	if err != nil || cm == nil {
		return err
	}
	startedAt := ""
	if cm.Annotations != nil {
		startedAt = cm.Annotations[capabilitiesCleanupStartedAnnotation]
	}
	disabled := boolString(false)
	flagsDisabled := cm.Data[capabilitiesKeyFeatureStoreEnabled] == disabled &&
		cm.Data[capabilitiesKeyDataRegistryEnabled] == disabled
	if flagsDisabled && startedAt != "" {
		return nil
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	cm.Data[capabilitiesKeyFeatureStoreEnabled] = disabled
	cm.Data[capabilitiesKeyDataRegistryEnabled] = disabled
	if startedAt == "" {
		cm.Annotations[capabilitiesCleanupStartedAnnotation] = time.Now().UTC().Format(time.RFC3339)
	}
	if err := rr.Client.Update(ctx, cm); err != nil {
		return fmt.Errorf("failed to write teardown intent into capabilities ConfigMap %s: %w", capabilitiesConfigMapName, err)
	}
	return nil
}

// deleteCapabilitiesConfigMap issues the ConfigMap delete without removing
// the module finalizer. The ConfigMap stays in Terminating, held by the
// module and feast operator finalizers, until both have completed cleanup.
func (m *Module) deleteCapabilitiesConfigMap(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	cm, err := m.getCapabilitiesConfigMap(ctx, rr)
	if err != nil || cm == nil {
		return err
	}
	if cm.DeletionTimestamp != nil {
		return nil // already deleting (finalizer re-run)
	}
	if err := rr.Client.Delete(ctx, cm); client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to delete capabilities ConfigMap %s: %w", capabilitiesConfigMapName, err)
	}
	return nil
}

// waitForFeastOperatorRelease blocks finalization — by returning an error the
// reconciler requeues and re-runs the finalizer — until the feast operator
// has removed its finalizer from the Terminating capabilities ConfigMap,
// signalling that its Data Registry cleanup finished. deletionTimestamp only
// confirms deletion has started. The wait is bounded by
// capabilitiesCleanupStartedAnnotation: after feastOperatorReleaseTimeout the
// module emits a Warning event and proceeds without the feast operator,
// leaving manual cleanup of the stuck Terminating ConfigMap as the recovery path.
func (m *Module) waitForFeastOperatorRelease(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)

	cm, err := m.getCapabilitiesConfigMap(ctx, rr)
	if err != nil || cm == nil {
		return err // gone: nothing to wait for
	}

	if !controllerutil.ContainsFinalizer(cm, feastOperatorCapabilitiesFinalizer) {
		// The feast operator never claimed the ConfigMap (older version) or
		// has already released it after cleaning up.
		return nil
	}

	if cm.DeletionTimestamp == nil {
		return fmt.Errorf("capabilities ConfigMap %s is not terminating yet", capabilitiesConfigMapName)
	}

	startedAt := ""
	if cm.Annotations != nil {
		startedAt = cm.Annotations[capabilitiesCleanupStartedAnnotation]
	}
	started, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return fmt.Errorf("capabilities ConfigMap %s cleanup start time is missing or invalid", capabilitiesConfigMapName)
	}
	heldSince := started.UTC().Format(time.RFC3339)
	if time.Since(started) < feastOperatorReleaseTimeout {
		return fmt.Errorf(
			"waiting for feast operator to release capabilities ConfigMap %s (finalizer %s held since %s)",
			capabilitiesConfigMapName, feastOperatorCapabilitiesFinalizer, heldSince,
		)
	}

	log.Info("Timed out waiting for feast operator to release capabilities ConfigMap; proceeding with uninstall",
		"configmap", capabilitiesConfigMapName,
		"namespace", m.cfg.ApplicationsNamespace,
		"finalizer", feastOperatorCapabilitiesFinalizer,
		"heldSince", heldSince,
	)
	if m.recorder != nil {
		m.recorder.Eventf(rr.Instance, nil, corev1.EventTypeWarning, capabilitiesCleanupTimeoutReason, "Uninstall",
			"Timed out waiting for finalizer %s on ConfigMap %s/%s (held since %s); uninstall continuing. For manual cleanup, remove only this finalizer and preserve any others.",
			feastOperatorCapabilitiesFinalizer,
			m.cfg.ApplicationsNamespace, capabilitiesConfigMapName, heldSince,
		)
	}
	return nil
}

// releaseCapabilitiesConfigMap removes the module finalizer from the
// Terminating capabilities ConfigMap, allowing the API server to complete
// the deletion once the feast operator finalizer is gone too.
func (m *Module) releaseCapabilitiesConfigMap(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	cm, err := m.getCapabilitiesConfigMap(ctx, rr)
	if err != nil || cm == nil {
		return err
	}
	if !controllerutil.ContainsFinalizer(cm, CapabilitiesConfigMapFinalizer) {
		return nil
	}
	controllerutil.RemoveFinalizer(cm, CapabilitiesConfigMapFinalizer)
	if err := rr.Client.Update(ctx, cm); err != nil {
		return fmt.Errorf("failed to remove finalizer from capabilities ConfigMap %s: %w", capabilitiesConfigMapName, err)
	}
	return nil
}
