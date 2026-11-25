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
	"path/filepath"
	"testing"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/conditions"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	dchTestNamespace = "applications"
	dchTestVersion   = "3.7.0"
)

func newDCHTestRequest(t *testing.T, enabled bool) (*Module, *odhtypes.ReconciliationRequest) {
	t.Helper()
	scheme := initCapabilitiesTestScheme()
	require.NoError(t, promv1.AddToScheme(scheme))
	owner := newTestFeastOperator()
	owner.UID = "data-module-uid"
	owner.Generation = 1
	state := componentApi.CapabilityRemoved
	if enabled {
		state = componentApi.CapabilityManaged
	}
	owner.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		DataConnectHub: &componentApi.DataConnectHubCapabilitySpec{
			CapabilitySpec: componentApi.CapabilitySpec{ManagementState: state},
		},
	}
	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(owner, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: PlatformConfigCMName, Namespace: dchTestNamespace},
		Data:       map[string]string{PlatformVersionKey: dchTestVersion},
	}).Build()
	aggregator, err := conditions.NewAggregator(fwapi.ConditionTypeReady,
		conditions.Dependent(dchReadyCondition, conditions.HealthyWhenTrue),
		conditions.Dependent("DeploymentsAvailable", conditions.HealthyWhenTrue))
	require.NoError(t, err)
	rr := &odhtypes.ReconciliationRequest{
		Client: cli, Instance: owner, Conditions: conditions.NewManager(owner, aggregator),
	}
	rr.Conditions.MarkTrue("DeploymentsAvailable")
	m := &Module{cfg: &moduleconfig.Config{
		ApplicationsNamespace: dchTestNamespace,
		PlatformType:          "OpenDataHub",
		ManifestsPath:         filepath.Join("..", "..", "..", "config", "manifests"),
	}}
	return m, rr
}

func TestDCHDisabledWithoutInstalledAPI(t *testing.T) {
	m, rr := newDCHTestRequest(t, false)
	m.reader = fake.NewClientBuilder().WithScheme(rr.Client.Scheme()).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: dchServiceGVK.Group, Kind: dchServiceGVK.Kind}}
		},
	}).Build()
	require.NoError(t, m.prepareDataConnectHub(context.Background(), rr))
	require.NoError(t, m.renderDataConnectHub(context.Background(), rr))
	require.Empty(t, rr.Resources)
	require.Equal(t, metav1.ConditionTrue, rr.Conditions.GetCondition(dchReadyCondition).Status)
}

func TestDCHApplicationProjection(t *testing.T) {
	m, rr := newDCHTestRequest(t, true)
	capability := rr.Instance.(*componentApi.FeastOperator).Spec.Capabilities.DataConnectHub
	capability.Gateway = &componentApi.DataConnectHubGatewaySpec{Name: "platform-gateway", Namespace: "ingress"}
	capability.TokenReviewAudiences = []string{"https://issuer.example"}
	require.NoError(t, m.prepareDataConnectHub(context.Background(), rr))
	require.Len(t, rr.Resources, 2)
	cm := rr.Resources[0]
	require.Equal(t, dchNamespaceODH, cm.GetNamespace())
	version, _, err := unstructured.NestedString(cm.Object, "data", PlatformVersionKey)
	require.NoError(t, err)
	require.Equal(t, dchTestVersion, version)
	service := rr.Resources[1]
	require.Equal(t, dchNamespaceODH, service.GetNamespace())
	require.Equal(t, dchServiceGVK, service.GroupVersionKind())
	require.True(t, ownedByDataModule(&service, rr.Instance.(*componentApi.FeastOperator)))
	spec, _, err := unstructured.NestedMap(service.Object, "spec")
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"gateway":              map[string]any{"name": "platform-gateway", "namespace": "ingress", "routeNamespace": dchTestNamespace},
		"tokenReviewAudiences": []any{"https://issuer.example"},
	}, spec)
}

func TestDCHRefusesUnownedResources(t *testing.T) {
	for _, resource := range []string{"application", "config"} {
		t.Run(resource, func(t *testing.T) {
			m, rr := newDCHTestRequest(t, true)
			var obj client.Object = newDCHService(dchNamespaceODH)
			if resource == "config" {
				obj = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: dchConfigName, Namespace: dchNamespaceODH}}
			}
			require.NoError(t, rr.Client.Create(context.Background(), obj))
			err := m.prepareDataConnectHub(context.Background(), rr)
			require.ErrorContains(t, err, "ownership conflict")
			require.Empty(t, rr.Resources)
			require.NoError(t, rr.Client.Get(context.Background(), client.ObjectKeyFromObject(obj), obj))
			require.Empty(t, obj.GetOwnerReferences())
		})
	}
}

func TestDCHCleanupWaitsForApplication(t *testing.T) {
	ctx := context.Background()
	m, rr := newDCHTestRequest(t, false)
	owner := rr.Instance.(*componentApi.FeastOperator)
	service := newDCHService(dchNamespaceODH)
	service.SetFinalizers([]string{"dataconnecthub.opendatahub.io/finalizer"})
	require.NoError(t, controllerutil.SetControllerReference(owner, service, rr.Client.Scheme()))
	require.NoError(t, rr.Client.Create(ctx, service))
	controller := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: dchDeploymentName, Namespace: dchTestNamespace}}
	require.NoError(t, rr.Client.Create(ctx, controller))
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "dch-controller-role"}}
	require.NoError(t, rr.Client.Create(ctx, role))

	require.Error(t, m.prepareDataConnectHub(ctx, rr))
	require.ErrorContains(t, m.cleanupClusterResources(ctx, rr), "waiting for DataConnectService cleanup")
	require.NoError(t, rr.Client.Get(ctx, client.ObjectKeyFromObject(controller), controller))
	require.NoError(t, rr.Client.Get(ctx, client.ObjectKeyFromObject(role), role))
	require.NoError(t, rr.Client.Get(ctx, client.ObjectKeyFromObject(service), service))
	require.False(t, service.GetDeletionTimestamp().IsZero())

	// Re-enable while cleanup is running: never recreate/adopt a terminating CR.
	owner.Spec.Capabilities.DataConnectHub.ManagementState = componentApi.CapabilityManaged
	require.ErrorContains(t, m.prepareDataConnectHub(ctx, rr), "previous DataConnectService deletion")
	owner.Spec.Capabilities.DataConnectHub.ManagementState = componentApi.CapabilityRemoved
	service.SetFinalizers(nil)
	require.NoError(t, rr.Client.Update(ctx, service))
	require.NoError(t, m.prepareDataConnectHub(ctx, rr))
	require.NoError(t, m.renderDataConnectHub(ctx, rr))
	require.Empty(t, rr.Resources, "DCH controller resources can now be garbage-collected")
}

func TestDCHDisablePreservesStandaloneApplication(t *testing.T) {
	m, rr := newDCHTestRequest(t, false)
	service := newDCHService(dchNamespaceODH)
	require.NoError(t, rr.Client.Create(context.Background(), service))
	require.NoError(t, m.prepareDataConnectHub(context.Background(), rr))
	require.NoError(t, rr.Client.Get(context.Background(), client.ObjectKeyFromObject(service), service))
	require.True(t, service.GetDeletionTimestamp().IsZero())
}

func setDCHTestImages(t *testing.T) {
	t.Helper()
	for _, name := range dchImageNames {
		t.Setenv(name, "mirror.example/"+name+"@sha256:0123456789abcdef")
	}
}

func TestDCHBundleRendering(t *testing.T) {
	m, rr := newDCHTestRequest(t, true)
	setDCHTestImages(t)
	require.NoError(t, m.prepareDataConnectHub(context.Background(), rr))
	require.NoError(t, m.renderDataConnectHub(context.Background(), rr))
	applications, controllers := 0, 0
	for i := range rr.Resources {
		obj := &rr.Resources[i]
		require.NotEqual(t, "Namespace", obj.GetKind(), "the platform owns the shared namespace")
		require.NotEqual(t, "DataConnectHub", obj.GetKind(), "there must not be another module CR")
		if obj.GetKind() == "DataConnectService" {
			applications++
		}
		if obj.GetKind() == "CustomResourceDefinition" {
			scope, _, err := unstructured.NestedString(obj.Object, "spec", "scope")
			require.NoError(t, err)
			require.Equal(t, "Namespaced", scope)
			require.Empty(t, obj.GetOwnerReferences())
		} else {
			require.True(t, ownedByDataModule(obj, rr.Instance.(*componentApi.FeastOperator)), obj.GetName())
		}
		if obj.GetKind() != "Deployment" {
			continue
		}
		controllers++
		require.Equal(t, dchDeploymentName, obj.GetName())
		require.Equal(t, dchTestNamespace, obj.GetNamespace())
		var deployment appsv1.Deployment
		require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &deployment))
		container := deployment.Spec.Template.Spec.Containers[0]
		require.Equal(t, "mirror.example/"+dchImageNames[0]+"@sha256:0123456789abcdef", container.Image)
		for _, name := range dchImageNames[1:] {
			found := false
			for _, env := range container.Env {
				if env.Name == name {
					found = true
					require.Equal(t, "mirror.example/"+name+"@sha256:0123456789abcdef", env.Value)
				}
			}
			require.True(t, found, name)
		}
	}
	require.Equal(t, 1, applications)
	require.Equal(t, 1, controllers)
}

func TestDCHMissingImagesFailsClosed(t *testing.T) {
	m, rr := newDCHTestRequest(t, true)
	for _, name := range dchImageNames {
		t.Setenv(name, "")
	}
	require.ErrorContains(t, m.renderDataConnectHub(context.Background(), rr), dchImageNames[0])
}

func TestDCHInfrastructureNamespaceSelection(t *testing.T) {
	for _, tc := range []struct {
		platform string
		override string
		want     string
	}{
		{"Open Data Hub", "", dchNamespaceODH},
		{"OpenShift AI Self-Managed", "", dchNamespaceRHOAI},
		{"OpenShift AI Cloud Service", "", dchNamespaceRHOAI},
		{"OpenShift AI Self-Managed", "custom-dch-infra", "custom-dch-infra"},
	} {
		t.Run(tc.platform+"/"+tc.override, func(t *testing.T) {
			m, rr := newDCHTestRequest(t, true)
			m.cfg.PlatformType = tc.platform
			owner := rr.Instance.(*componentApi.FeastOperator)
			owner.Spec.Capabilities.DataConnectHub.Namespace = tc.override
			require.NoError(t, m.prepareDataConnectHub(t.Context(), rr))
			for _, obj := range rr.Resources {
				require.Equal(t, tc.want, obj.GetNamespace())
			}
			ns := &corev1.Namespace{}
			require.NoError(t, rr.Client.Get(t.Context(), client.ObjectKey{Name: tc.want}, ns))
			require.Empty(t, ns.OwnerReferences)
		})
	}
}

func TestDCHNamespaceRemovalFindsLegacyApplication(t *testing.T) {
	m, rr := newDCHTestRequest(t, true)
	owner := rr.Instance.(*componentApi.FeastOperator)
	legacy := newDCHService(dchTestNamespace)
	require.NoError(t, controllerutil.SetControllerReference(owner, legacy, rr.Client.Scheme()))
	require.NoError(t, rr.Client.Create(t.Context(), legacy))
	require.ErrorContains(t, m.prepareDataConnectHub(t.Context(), rr), "set Removed and migrate")
	owner.Spec.Capabilities.DataConnectHub.ManagementState = componentApi.CapabilityRemoved
	pending, err := m.deleteDCHService(t.Context(), rr)
	require.NoError(t, err)
	require.True(t, pending)
	require.Error(t, rr.Client.Get(t.Context(), client.ObjectKeyFromObject(legacy), legacy))
}

func TestDCHBundlePreservesSharedServiceCA(t *testing.T) {
	ctx := context.Background()
	m, rr := newDCHTestRequest(t, true)
	setDCHTestImages(t)
	shared := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-service-ca.crt", Namespace: dchTestNamespace,
			Annotations: map[string]string{"service.beta.openshift.io/inject-cabundle": "true"},
		},
		Data: map[string]string{"service-ca.crt": "existing-shared-ca"},
	}
	require.NoError(t, rr.Client.Create(ctx, shared))
	before := shared.DeepCopy()
	require.NoError(t, m.prepareDataConnectHub(ctx, rr))
	require.NoError(t, m.renderDataConnectHub(ctx, rr))
	foundBundle, foundMount, foundMonitor, foundMetrics := false, false, false, false
	for i := range rr.Resources {
		obj := &rr.Resources[i]
		switch obj.GetKind() {
		case "ConfigMap":
			require.NotEqual(t, shared.Name, obj.GetName(), "never adopt or garbage-collect the shared CA bundle")
			if obj.GetName() == "dc-controller-service-ca" {
				foundBundle = true
				require.Equal(t, "true", obj.GetAnnotations()["service.beta.openshift.io/inject-cabundle"])
				require.True(t, ownedByDataModule(obj, rr.Instance.(*componentApi.FeastOperator)))
			}
		case "Deployment":
			var deployment appsv1.Deployment
			require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &deployment))
			for _, volume := range deployment.Spec.Template.Spec.Volumes {
				if volume.Name == "metrics-certs" {
					require.NotNil(t, volume.Secret)
					var keys []string
					for _, item := range volume.Secret.Items {
						keys = append(keys, item.Key)
					}
					require.ElementsMatch(t, []string{"tls.crt", "tls.key"}, keys,
						"OpenShift serving-cert Secrets do not contain ca.crt")
					foundMetrics = true
				}
				if volume.Name == "service-ca" {
					require.NotNil(t, volume.ConfigMap)
					require.Equal(t, "dc-controller-service-ca", volume.ConfigMap.Name)
					foundMount = true
				}
			}
		case "ServiceMonitor":
			var monitor promv1.ServiceMonitor
			require.NoError(t, runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &monitor))
			require.NotEmpty(t, monitor.Spec.Endpoints)
			require.NotNil(t, monitor.Spec.Endpoints[0].TLSConfig)
			require.NotNil(t, monitor.Spec.Endpoints[0].TLSConfig.CA.ConfigMap)
			require.Equal(t, "dc-controller-service-ca", monitor.Spec.Endpoints[0].TLSConfig.CA.ConfigMap.Name)
			foundMonitor = true
		}
	}
	require.True(t, foundBundle)
	require.True(t, foundMount)
	require.True(t, foundMonitor)
	require.True(t, foundMetrics)
	require.NoError(t, rr.Client.Get(ctx, client.ObjectKeyFromObject(shared), shared))
	require.Equal(t, before, shared, "shared CA contents, annotations and ownership must remain untouched")
}

func TestDCHReadinessAndUpgradeHandshake(t *testing.T) {
	for _, tc := range []struct {
		name, childReady, degraded, version, wantReason string
		stale, controllerPending, feastPending          bool
	}{
		{name: "ready", childReady: "True", degraded: "False", version: dchTestVersion, wantReason: "Ready"},
		{name: "database prerequisite", childReady: "False", degraded: "True", version: "3.6.0", wantReason: "DatabaseSecretMissing"},
		{name: "degraded", childReady: "True", degraded: "True", version: dchTestVersion, wantReason: "GatewayDegraded"},
		{name: "stale status", childReady: "True", degraded: "False", version: dchTestVersion, stale: true, wantReason: "StaleStatus"},
		{name: "upgrade pending", childReady: "True", degraded: "False", version: "3.6.0", wantReason: "UpgradePending"},
		{name: "controller rollout", childReady: "True", degraded: "False", version: dchTestVersion, controllerPending: true, wantReason: "ControllerUnavailable"},
		{name: "Feast controller rollout", childReady: "True", degraded: "False", version: dchTestVersion, feastPending: true, wantReason: "Ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			m, rr := newDCHTestRequest(t, true)
			owner := rr.Instance.(*componentApi.FeastOperator)
			setPlatformRelease(owner, "3.6.0")
			service := newDCHService(dchNamespaceODH)
			service.SetGeneration(2)
			observed := int64(2)
			if tc.stale {
				observed = 1
			}
			service.Object["status"] = map[string]any{
				"observedGeneration": observed,
				"conditions": []any{
					map[string]any{"type": "Ready", "status": tc.childReady, "reason": "DatabaseSecretMissing", "message": "Application status"},
					map[string]any{"type": "ProvisioningSucceeded", "status": "True"},
					map[string]any{"type": "Degraded", "status": tc.degraded, "reason": "GatewayDegraded", "message": "Gateway status"},
				},
				"releases": []any{
					map[string]any{"name": platformReleaseName, "version": tc.version},
					map[string]any{"name": "rest-service", "version": "1.0"},
				},
			}
			require.NoError(t, controllerutil.SetControllerReference(owner, service, rr.Client.Scheme()))
			require.NoError(t, rr.Client.Create(ctx, service))
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: dchDeploymentName, Namespace: dchTestNamespace, Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: ptr.To(int32(1))},
				Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, ReadyReplicas: 1, UpdatedReplicas: 1},
			}
			if tc.controllerPending {
				deployment.Status.UpdatedReplicas = 0
			}
			require.NoError(t, controllerutil.SetControllerReference(owner, deployment, rr.Client.Scheme()))
			feastDeployment := deployment.DeepCopy()
			feastDeployment.Name = deploymentName
			feastDeployment.Status.UpdatedReplicas = 1
			if tc.feastPending {
				feastDeployment.Status.UpdatedReplicas = 0
			}
			// Preload status; Create on the fake client follows status-subresource semantics.
			rr.Client = fake.NewClientBuilder().WithScheme(rr.Client.Scheme()).WithObjects(
				owner, service, deployment, feastDeployment, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: PlatformConfigCMName, Namespace: dchTestNamespace},
					Data:       map[string]string{PlatformVersionKey: dchTestVersion},
				},
			).Build()
			require.NoError(t, m.reportDataConnectHubStatus(ctx, rr))
			condition := rr.Conditions.GetCondition(dchReadyCondition)
			require.Equal(t, tc.wantReason, condition.Reason)
			require.NoError(t, m.reconcilePlatformVersion(ctx, rr))
			if tc.wantReason == "Ready" && !tc.feastPending {
				require.Equal(t, metav1.ConditionTrue, condition.Status)
				require.Equal(t, dchTestVersion, getPlatformRelease(owner))
			} else if tc.feastPending {
				require.Equal(t, "3.6.0", getPlatformRelease(owner))
				require.Equal(t, metav1.ConditionFalse, rr.Conditions.GetCondition("DeploymentsAvailable").Status)
			} else {
				require.Equal(t, metav1.ConditionFalse, condition.Status)
				require.Equal(t, "3.6.0", getPlatformRelease(owner))
			}
			if tc.name == "degraded" {
				require.Equal(t, metav1.ConditionTrue, rr.Conditions.GetCondition(fwapi.ConditionTypeDegraded).Status)
			}
			owner.Spec.Capabilities.DataConnectHub.ManagementState = componentApi.CapabilityRemoved
			require.NoError(t, m.reportDataConnectHubStatus(ctx, rr))
			for _, release := range owner.Status.Releases {
				require.NotContains(t, release.Name, dchReleasePrefix)
			}
		})
	}
}
