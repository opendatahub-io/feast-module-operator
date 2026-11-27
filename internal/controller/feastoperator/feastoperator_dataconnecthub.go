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
	"fmt"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	odherrors "github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/errors"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/render/kustomize"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

const (
	dchServiceName    = "default-dataconnectservice"
	dchConfigName     = "opendatahub-dataconnecthub-config"
	dchDeploymentName = "dc-controller-manager"
	dchCapabilityKey  = "data.opendatahub.io/capability"
	dchCapability     = "dataConnectHub"
	dchReadyCondition = fwapi.ConditionType("DataConnectHubReady")
	dchReleasePrefix  = "dataconnecthub/"
	dchRetryInterval  = 10 * time.Second
	dchNamespaceODH   = "odh-dch-infra"
	dchNamespaceRHOAI = "redhat-dch-infra"
)

var dchServiceGVK = schema.GroupVersionKind{
	Group: "dataconnecthub.opendatahub.io", Version: "v1alpha1", Kind: "DataConnectService",
}

var dchImageNames = []string{
	"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_CONTROLLER_IMAGE",
	"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_REST_IMAGE",
	"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_FLIGHT_IMAGE",
	"RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE",
}

func newDCHService(namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(dchServiceGVK)
	obj.SetName(dchServiceName)
	obj.SetNamespace(namespace)
	return obj
}

func (m *Module) dchReader(rr *odhtypes.ReconciliationRequest) client.Reader {
	if m.reader != nil {
		return m.reader
	}
	return rr.Client
}

func (m *Module) getDCHService(ctx context.Context, rr *odhtypes.ReconciliationRequest) (*unstructured.Unstructured, error) {
	obj := newDCHService(m.dchNamespace(rr.Instance.(*componentApi.FeastOperator)))
	if err := m.dchReader(rr).Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if k8serr.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading DataConnectService: %w", err)
	}
	return obj, nil
}

func (m *Module) dchNamespace(owner *componentApi.FeastOperator) string {
	if owner.Spec.Capabilities != nil && owner.Spec.Capabilities.DataConnectHub != nil && owner.Spec.Capabilities.DataConnectHub.Namespace != "" {
		return owner.Spec.Capabilities.DataConnectHub.Namespace
	}
	if m.cfg.PlatformType == string(cluster.SelfManagedRhoai) || m.cfg.PlatformType == string(cluster.ManagedRhoai) {
		return dchNamespaceRHOAI
	}
	return dchNamespaceODH
}

// Preserve namespaces on removal: they may hold databases and user-owned data.
func (m *Module) ensureDCHNamespace(ctx context.Context, rr *odhtypes.ReconciliationRequest, namespace string) error {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if err := rr.Client.Get(ctx, client.ObjectKeyFromObject(ns), ns); err == nil {
		if !ns.DeletionTimestamp.IsZero() {
			return fmt.Errorf("DCH namespace %s is terminating", namespace)
		}
		return nil
	} else if !k8serr.IsNotFound(err) {
		return fmt.Errorf("reading DCH namespace: %w", err)
	}
	if err := rr.Client.Create(ctx, ns); err != nil && !k8serr.IsAlreadyExists(err) {
		return fmt.Errorf("creating DCH namespace: %w", err)
	}
	return nil
}

func ownedByDataModule(obj client.Object, owner *componentApi.FeastOperator) bool {
	ref := metav1.GetControllerOf(obj)
	return ref != nil && ref.UID == owner.UID && ref.Kind == componentApi.FeastOperatorKind &&
		ref.APIVersion == componentApi.GroupVersion.String()
}

func (m *Module) checkDCHOwnership(ctx context.Context, rr *odhtypes.ReconciliationRequest, obj client.Object) error {
	current := obj.DeepCopyObject().(client.Object)
	if err := m.dchReader(rr).Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
		if k8serr.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("checking DCH resource ownership: %w", err)
	}
	owner := rr.Instance.(*componentApi.FeastOperator)
	if !ownedByDataModule(current, owner) {
		return fmt.Errorf("DCH ownership conflict for %s/%s: refusing to adopt an existing resource not controlled by this FeastOperator", obj.GetNamespace(), obj.GetName())
	}
	return nil
}

func markDCH(rr *odhtypes.ReconciliationRequest, status metav1.ConditionStatus, reason, message string) {
	if rr.Conditions != nil {
		rr.Conditions.Mark(dchReadyCondition, status,
			conditions.WithReason(reason), conditions.WithMessage(message),
			conditions.WithObservedGeneration(rr.Instance.GetGeneration()))
	}
}

// prepareDataConnectHub is before rendering/deployment/GC. Disabling DCH pauses
// this pipeline until its application finalizer completes, keeping dc-controller
// and its RBAC alive. The shared Feast operator installation is unchanged.
func (m *Module) prepareDataConnectHub(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	owner := rr.Instance.(*componentApi.FeastOperator)
	if !owner.Spec.DataConnectHubEnabled() {
		pending, err := m.deleteDCHService(ctx, rr)
		if err != nil {
			return err
		}
		if pending {
			markDCH(rr, metav1.ConditionFalse, "Removing", "Waiting for DataConnectService cleanup before removing dc-controller")
			return odherrors.NewStopError("waiting for DataConnectService cleanup").WithRequeueAfter(dchRetryInterval)
		}
		markDCH(rr, metav1.ConditionTrue, "Removed", "Data Connect Hub capability is disabled")
		if err := m.deleteDCHConfig(ctx, rr); err != nil {
			return err
		}
		return nil
	}
	namespace := m.dchNamespace(owner)
	var installations unstructured.UnstructuredList
	installations.SetGroupVersionKind(dchServiceGVK.GroupVersion().WithKind("DataConnectServiceList"))
	if err := m.dchReader(rr).List(ctx, &installations); err != nil && !meta.IsNoMatchError(err) && !k8serr.IsNotFound(err) {
		return fmt.Errorf("checking existing DCH installations: %w", err)
	}
	for i := range installations.Items {
		installed := &installations.Items[i]
		if installed.GetNamespace() != namespace {
			return fmt.Errorf("DCH already exists in namespace %s; set Removed and migrate it before installing into %s", installed.GetNamespace(), namespace)
		}
	}
	markDCH(rr, metav1.ConditionFalse, "Reconciling", "Reconciling Data Connect Hub")
	service, err := m.getDCHService(ctx, rr)
	if err != nil {
		return err
	}
	if service != nil {
		if !ownedByDataModule(service, owner) {
			return fmt.Errorf("DCH ownership conflict: DataConnectService %s/%s is not controlled by this FeastOperator", service.GetNamespace(), service.GetName())
		}
		if !service.GetDeletionTimestamp().IsZero() {
			return odherrors.NewStopError("waiting for the previous DataConnectService deletion").WithRequeueAfter(dchRetryInterval)
		}
	}
	version, err := m.getPlatformVersion(ctx, rr.Client)
	if k8serr.IsNotFound(err) || err == nil && version == "" {
		return odherrors.NewStopError("waiting for odh-data-config platformVersion").WithRequeueAfter(dchRetryInterval)
	}
	if err != nil {
		return err
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: dchConfigName, Namespace: namespace},
		Data: map[string]string{
			PlatformVersionKey: version, "distribution.name": m.cfg.PlatformType, "distribution.version": version,
			"gateway.routeNamespace": m.cfg.ApplicationsNamespace,
		},
	}
	if err := m.ensureDCHNamespace(ctx, rr, namespace); err != nil {
		return err
	}
	if err := m.checkDCHOwnership(ctx, rr, cm); err != nil {
		return err
	}
	if err := controllerutil.SetControllerReference(owner, cm, rr.Client.Scheme()); err != nil {
		return err
	}
	if err := rr.AddResources(cm); err != nil {
		return err
	}

	service = newDCHService(namespace)
	spec := map[string]any{}
	capability := owner.Spec.Capabilities.DataConnectHub
	if capability.Gateway != nil {
		spec["gateway"] = map[string]any{"name": capability.Gateway.Name, "namespace": capability.Gateway.Namespace, "routeNamespace": m.cfg.ApplicationsNamespace}
	}
	if capability.TokenReviewAudiences != nil {
		audiences := make([]any, len(capability.TokenReviewAudiences))
		for i, audience := range capability.TokenReviewAudiences {
			audiences[i] = audience
		}
		spec["tokenReviewAudiences"] = audiences
	}
	service.Object["spec"] = spec
	if err := controllerutil.SetControllerReference(owner, service, rr.Client.Scheme()); err != nil {
		return err
	}
	// SSA owns only gateway and audiences. Application sizing/trace/connector
	// overrides are deliberately absent and remain owned by their field manager.
	return rr.AddResources(service)
}

// renderDataConnectHub renders the controller bundle separately so labels,
// namespace filtering, image injection, and conflict checks cannot affect Feast.
func (m *Module) renderDataConnectHub(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	if !rr.Instance.(*componentApi.FeastOperator).Spec.DataConnectHubEnabled() {
		return nil
	}
	images := make(map[string]string, len(dchImageNames))
	for _, name := range dchImageNames {
		value := os.Getenv(name)
		if value == "" {
			return fmt.Errorf("DCH requires %s: refusing to use bundled upstream image defaults", name)
		}
		images[name] = value
	}
	overlay := "default"
	if m.cfg.PlatformType != string(cluster.XKS) {
		overlay = "overlays/openshift"
	}
	sub := *rr
	sub.Resources = nil
	sub.Manifests = []odhtypes.ManifestInfo{{Path: m.cfg.ManifestsPath, ContextDir: "dataconnecthub", SourcePath: overlay}}
	render := kustomize.NewAction(
		kustomize.WithNamespace(m.cfg.ApplicationsNamespace),
		kustomize.WithLabel(labels.ODH.Component(componentName), labels.True),
		kustomize.WithLabel(dchCapabilityKey, dchCapability),
		kustomize.WithCache(false),
	)
	if err := render(ctx, &sub); err != nil {
		return fmt.Errorf("rendering DCH controller manifests: %w", err)
	}
	foundController := false
	for i := range sub.Resources {
		obj := &sub.Resources[i]
		if obj.GetKind() == "Namespace" {
			// The platform owns the shared applications namespace.
			continue
		}
		if err := injectDCHImages(obj, images); err != nil {
			return err
		}
		if obj.GetKind() == "Deployment" && obj.GetName() == dchDeploymentName {
			foundController = true
		}
		if obj.GetKind() != "CustomResourceDefinition" {
			if err := m.checkDCHOwnership(ctx, rr, obj); err != nil {
				return err
			}
			if err := controllerutil.SetControllerReference(rr.Instance, obj, rr.Client.Scheme()); err != nil {
				return err
			}
		}
		rr.Resources = append(rr.Resources, *obj)
	}
	if !foundController {
		return fmt.Errorf("DCH bundle does not contain Deployment %s", dchDeploymentName)
	}
	rr.Generated = true
	return nil
}

func injectDCHImages(obj *unstructured.Unstructured, images map[string]string) error {
	if obj.GetKind() == "ConfigMap" {
		data, found, err := unstructured.NestedStringMap(obj.Object, "data")
		if err != nil || !found {
			return err
		}
		for key := range data {
			if image, ok := images[key]; ok {
				data[key] = image
			}
		}
		return unstructured.SetNestedStringMap(obj.Object, data, "data")
	}
	if obj.GetKind() != "Deployment" || obj.GetName() != dchDeploymentName {
		return nil
	}
	containers, _, err := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if err != nil {
		return err
	}
	for _, item := range containers {
		container, ok := item.(map[string]any)
		if !ok || container["name"] != "manager" {
			continue
		}
		container["image"] = images[dchImageNames[0]]
		env, _, err := unstructured.NestedSlice(container, "env")
		if err != nil {
			return err
		}
		for _, name := range dchImageNames[1:] {
			var updated bool
			for _, item := range env {
				entry, ok := item.(map[string]any)
				if ok && entry["name"] == name {
					delete(entry, "valueFrom")
					entry["value"] = images[name]
					updated = true
				}
			}
			if !updated {
				env = append(env, map[string]any{"name": name, "value": images[name]})
			}
		}
		container["env"] = env
		return unstructured.SetNestedSlice(obj.Object, containers, "spec", "template", "spec", "containers")
	}
	return fmt.Errorf("DCH controller Deployment does not contain manager container")
}

// deleteDCHService never adopts/deletes an unrelated standalone application.
// A true result means the caller must keep dc-controller and its RBAC running.
func (m *Module) deleteDCHService(ctx context.Context, rr *odhtypes.ReconciliationRequest) (bool, error) {
	var services unstructured.UnstructuredList
	services.SetGroupVersionKind(dchServiceGVK.GroupVersion().WithKind("DataConnectServiceList"))
	if err := m.dchReader(rr).List(ctx, &services); err != nil {
		if k8serr.IsNotFound(err) || meta.IsNoMatchError(err) {
			return false, nil
		}
		return false, fmt.Errorf("listing DCH applications for cleanup: %w", err)
	}
	pending := false
	for i := range services.Items {
		service := &services.Items[i]
		if !ownedByDataModule(service, rr.Instance.(*componentApi.FeastOperator)) {
			continue
		}
		pending = true
		if service.GetDeletionTimestamp().IsZero() {
			uid := service.GetUID()
			if err := rr.Client.Delete(ctx, service, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); client.IgnoreNotFound(err) != nil {
				return false, fmt.Errorf("deleting DCH application: %w", err)
			}
		}
	}
	return pending, nil
}

func (m *Module) deleteDCHConfig(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	var configs corev1.ConfigMapList
	if err := m.dchReader(rr).List(ctx, &configs); err != nil {
		return fmt.Errorf("listing DCH configuration for cleanup: %w", err)
	}
	for i := range configs.Items {
		cm := &configs.Items[i]
		if cm.Name == dchConfigName && ownedByDataModule(cm, rr.Instance.(*componentApi.FeastOperator)) {
			uid := cm.UID
			if err := rr.Client.Delete(ctx, cm, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}); client.IgnoreNotFound(err) != nil {
				return fmt.Errorf("deleting DCH configuration: %w", err)
			}
		}
	}
	return nil
}

func dchCondition(obj *unstructured.Unstructured, conditionType string) (string, string, string) {
	items, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, item := range items {
		condition, ok := item.(map[string]any)
		if ok && condition["type"] == conditionType {
			status, _ := condition["status"].(string)
			reason, _ := condition["reason"].(string)
			message, _ := condition["message"].(string)
			return status, reason, message
		}
	}
	return "", "ConditionUnavailable", "Waiting for DataConnectService status"
}

func deploymentRolloutCurrent(deployment *appsv1.Deployment) bool {
	desired := int32(1)
	if deployment.Spec.Replicas != nil {
		desired = *deployment.Spec.Replicas
	}
	return deployment.DeletionTimestamp == nil && desired > 0 &&
		deployment.Status.ObservedGeneration >= deployment.Generation &&
		deployment.Status.UpdatedReplicas >= desired && deployment.Status.ReadyReplicas >= desired
}

func (m *Module) reportDataConnectHubStatus(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	owner := rr.Instance.(*componentApi.FeastOperator)
	if rr.Conditions != nil {
		rr.Conditions.MarkFalse(fwapi.ConditionTypeDegraded,
			conditions.WithReason("Reconciling"), conditions.WithObservedGeneration(owner.Generation))
	}
	// releases.NewAction refreshes module metadata first; remove any child entries
	// before appending the current snapshot so disable/re-enable cannot leak releases.
	releases := owner.Status.Releases[:0]
	for _, release := range owner.Status.Releases {
		if !strings.HasPrefix(release.Name, dchReleasePrefix) {
			releases = append(releases, release)
		}
	}
	owner.Status.Releases = releases
	if !owner.Spec.DataConnectHubEnabled() {
		markDCH(rr, metav1.ConditionTrue, "Removed", "Data Connect Hub capability is disabled")
		return nil
	}
	service, err := m.getDCHService(ctx, rr)
	if err != nil {
		return err
	}
	if service == nil || !ownedByDataModule(service, owner) || !service.GetDeletionTimestamp().IsZero() {
		markDCH(rr, metav1.ConditionFalse, "WaitingForDataConnectService", "Waiting for the managed DataConnectService")
		return nil
	}
	observed, _, _ := unstructured.NestedInt64(service.Object, "status", "observedGeneration")
	if observed < service.GetGeneration() {
		markDCH(rr, metav1.ConditionFalse, "StaleStatus", "DataConnectService has not observed its desired generation")
		return nil
	}
	if degraded, reason, message := dchCondition(service, "Degraded"); degraded == string(metav1.ConditionTrue) && rr.Conditions != nil {
		rr.Conditions.MarkTrue(fwapi.ConditionTypeDegraded,
			conditions.WithReason(reason), conditions.WithMessage(message), conditions.WithObservedGeneration(owner.Generation))
	}
	for _, name := range []string{"Ready", "ProvisioningSucceeded", "Degraded"} {
		status, reason, message := dchCondition(service, name)
		expected := string(metav1.ConditionTrue)
		if name == "Degraded" {
			expected = string(metav1.ConditionFalse)
		}
		if status != expected {
			markDCH(rr, metav1.ConditionFalse, reason, message)
			return nil
		}
	}
	deployment := &appsv1.Deployment{}
	if err := m.dchReader(rr).Get(ctx, client.ObjectKey{Name: dchDeploymentName, Namespace: m.cfg.ApplicationsNamespace}, deployment); err != nil {
		if !k8serr.IsNotFound(err) {
			return err
		}
		markDCH(rr, metav1.ConditionFalse, "ControllerUnavailable", "Waiting for dc-controller")
		return nil
	}
	if !ownedByDataModule(deployment, owner) || !deploymentRolloutCurrent(deployment) {
		markDCH(rr, metav1.ConditionFalse, "ControllerUnavailable", "Waiting for the current dc-controller rollout")
		return nil
	}
	version, err := m.getPlatformVersion(ctx, rr.Client)
	if err != nil {
		return err
	}
	childReleases, _, _ := unstructured.NestedSlice(service.Object, "status", "releases")
	acknowledged := false
	for _, item := range childReleases {
		release, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := release["name"].(string)
		childVersion, _ := release["version"].(string)
		if name == platformReleaseName {
			acknowledged = version != "" && childVersion == version
			continue
		}
		if name != "" {
			repoURL, _ := release["repoUrl"].(string)
			owner.Status.Releases = append(owner.Status.Releases, common.ComponentRelease{
				Name: dchReleasePrefix + name, Version: childVersion, RepoURL: repoURL,
			})
		}
	}
	if !acknowledged {
		markDCH(rr, metav1.ConditionFalse, "UpgradePending", "Waiting for DataConnectService to acknowledge the platform version")
		return nil
	}
	markDCH(rr, metav1.ConditionTrue, "Ready", "Data Connect Hub is ready")
	return nil
}
