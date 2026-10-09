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
	capabilitiesConfigMapName      = "feast-capabilities-config"
	capabilitiesConfigMapFinalizer = "components.platform.opendatahub.io/feast-capabilities"

	capabilitiesKeyFeatureStoreEnabled = "featureStoreEnabled"
	capabilitiesKeyDataRegistryEnabled = "dataRegistryEnabled"
	capabilitiesKeyDataRegistryNS      = "dataRegistryNamespace"

	capabilitiesHashAnnotation = "feast.dev/capabilities-hash"
)

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
			// Let an explicit deletion finish, then recreate the ConfigMap on retry.
			controllerutil.RemoveFinalizer(cm, capabilitiesConfigMapFinalizer)
			return nil
		}
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels[labels.ODH.Component(componentName)] = labels.True
		controllerutil.AddFinalizer(cm, capabilitiesConfigMapFinalizer)
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
		return fmt.Errorf("capabilities ConfigMap %s is being deleted; waiting to recreate it", capabilitiesConfigMapName)
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
