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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/status/releases"
	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"

	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()

	cfg := &moduleconfig.Config{
		PlatformType:          string(cluster.OpenDataHub),
		PlatformVersion:       "1.0.0",
		ManifestsPath:         "/manifests",
		ApplicationsNamespace: "test-ns",
	}

	m, err := NewModule(cfg)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return m
}

func newTestRR(obj *componentApi.FeastOperator) *odhtypes.ReconciliationRequest {
	return &odhtypes.ReconciliationRequest{
		Instance:          obj,
		ManifestsBasePath: "/manifests",
		Release: (&moduleconfig.Config{
			PlatformType:    string(cluster.OpenDataHub),
			PlatformVersion: "1.0.0",
		}).Release(),
	}
}

func newTestFeastOperator() *componentApi.FeastOperator {
	return &componentApi.FeastOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentApi.FeastOperatorInstanceName,
		},
	}
}

func TestImageParamMapIncludesKubeRBACProxy(t *testing.T) {
	g := NewWithT(t)
	g.Expect(imageParamMap).To(HaveKey("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"))
	g.Expect(imageParamMap["RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"]).To(
		Equal("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"),
	)
}

func TestNewModuleProjectsKubeRBACProxyImage(t *testing.T) {
	const image = "registry.example.com/odh-kube-rbac-proxy@sha256:1234"
	t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", image)
	for _, tc := range []struct {
		name, platform, overlay string
	}{
		{name: "ODH", platform: string(cluster.OpenDataHub), overlay: overlayODH},
		{name: "RHOAI", platform: string(cluster.SelfManagedRhoai), overlay: overlayRhoai},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifests := t.TempDir()
			path := filepath.Join(manifests, componentName, tc.overlay, "params.env")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE=default\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := NewModule(&moduleconfig.Config{
				PlatformType:  tc.platform,
				ManifestsPath: manifests,
			}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE="+image) {
				t.Fatalf("proxy image was not projected into %s: %s", path, data)
			}
		})
	}
}

func TestNewModule(t *testing.T) {
	g := NewWithT(t)

	cfg := &moduleconfig.Config{
		PlatformType:    string(cluster.OpenDataHub),
		PlatformVersion: "1.0.0",
		ManifestsPath:   "/manifests",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(m.cfg).To(Equal(cfg))
	g.Expect(m.manifestInfo.Path).To(Equal(cfg.ManifestsPath))
	g.Expect(m.manifestInfo.ContextDir).To(Equal(componentName))
	g.Expect(m.manifestInfo.SourcePath).To(Equal(overlayODH))
}

func TestInitialize(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	rr := newTestRR(obj)

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(HaveLen(1))
	g.Expect(rr.Manifests[0].Path).To(Equal("/manifests"))
	g.Expect(rr.Manifests[0].ContextDir).To(Equal(componentName))
	g.Expect(rr.Manifests[0].SourcePath).To(Equal(overlayODH))
}

func TestUpgradeIfNeededFreshInstall(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestUpgradeIfNeededSameVersion(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	setPlatformRelease(obj, "1.0.0")
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsNoOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	// No OIDC set on the CR
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	// Should succeed and write empty OIDC_ISSUER_URL
	g.Expect(m.setKustomizedParams(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsWithOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	obj.Spec.OIDC = &common.GatewayOIDCSpec{IssuerURL: "https://issuer.example.com"}
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	g.Expect(m.setKustomizedParams(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsInvalidOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	obj.Spec.OIDC = &common.GatewayOIDCSpec{IssuerURL: "not-a-url"}
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	err := m.setKustomizedParams(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("invalid OIDC issuer URL"))
}

func TestGetSetPlatformRelease(t *testing.T) {
	g := NewWithT(t)

	obj := newTestFeastOperator()

	// Initially empty
	g.Expect(getPlatformRelease(obj)).To(Equal(""))

	// Set a version
	setPlatformRelease(obj, "2.20.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.20.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(1))
	g.Expect(obj.Status.Releases[0].Name).To(Equal("platform"))

	// Update the version
	setPlatformRelease(obj, "2.21.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.21.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(1))

	// Simulate releases.NewAction() overwriting status.releases with
	// component releases, then setPlatformRelease appending the platform
	// entry — this mirrors the actual controller action ordering.
	obj.SetReleaseStatus(common.ComponentReleaseStatus{
		Releases: []common.ComponentRelease{
			{Name: "Feast", Version: "0.64.0"},
		},
	})
	g.Expect(getPlatformRelease(obj)).To(Equal(""))
	setPlatformRelease(obj, "2.21.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.21.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(2))
}

// TestReleasesActionAcceptsFeastOperator ensures FeastOperator satisfies
// WithReleases for framework releases.NewAction. Wrong accessor signatures
// compile against PlatformObject but fail the runtime type assert and
// short-circuit reconciliation before DeploymentsAvailable is set.
func TestReleasesActionAcceptsFeastOperator(t *testing.T) {
	g := NewWithT(t)

	obj := newTestFeastOperator()
	want := fwapi.ComponentReleaseStatus{
		Releases: []fwapi.ComponentRelease{
			{Name: "Feast", Version: "0.64.0"},
		},
	}

	action := releases.NewAction(releases.WithComponentReleaseStatus(want))
	err := action(context.Background(), &fwtypes.ReconciliationRequest{Instance: obj})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(obj.GetReleaseStatus().Releases).To(Equal(want.Releases))
}

func TestCleanupClusterResourcesRunsWhenCapabilityAbsent(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	// No spec.capabilities set — cleanup should proceed regardless of env defaults.
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   true,
			DataRegistryEnabled:   false,
		},
	}

	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.cleanupClusterResources(context.Background(), rr)).To(Succeed())
}

func TestCleanupClusterResourcesRunsEvenWhenCapabilitiesManaged(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityRemoved},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
			Labels: map[string]string{
				labels.ODH.Component(componentName): labels.True,
			},
			Finalizers: []string{CapabilitiesConfigMapFinalizer},
		},
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
		Name: dataRegistryNamespaceName,
		Labels: map[string]string{
			dataRegistryEnabledLabelKey: dataRegistryEnabledLabelValue,
			"user-label":                "preserved",
		},
	}}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm, ns).Build()

	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   false,
			DataRegistryEnabled:   false,
		},
	}

	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	// Finalizer must complete even when capabilities are still Managed.
	// Deferring creates a deadlock: the CR is being deleted, so its spec
	// will never change, but the finalizer waits for Removed.
	g.Expect(m.cleanupClusterResources(context.Background(), rr)).To(Succeed())

	// ConfigMap should be deleted
	getErr := cl.Get(context.Background(), client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
	g.Expect(k8serr.IsNotFound(getErr)).To(BeTrue())
	g.Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(ns), ns)).To(Succeed())
	g.Expect(ns.Labels).NotTo(HaveKey(dataRegistryEnabledLabelKey))
	g.Expect(ns.Labels["user-label"]).To(Equal("preserved"))
}

func TestCleanupClusterResourcesRunsWhenBothCapabilitiesRemoved(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
			Labels: map[string]string{
				labels.ODH.Component(componentName): labels.True,
			},
		},
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "feast-crb",
			Labels: map[string]string{
				labels.ODH.Component(componentName): labels.True,
			},
		},
	}
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "feast-cr",
			Labels: map[string]string{
				labels.ODH.Component(componentName): labels.True,
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm, crb, cr).Build()

	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   false,
			DataRegistryEnabled:   false,
		},
	}

	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.cleanupClusterResources(context.Background(), rr)).To(Succeed())

	getErr := cl.Get(context.Background(), client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
	g.Expect(client.IgnoreNotFound(getErr)).To(Succeed())

	crbList := &rbacv1.ClusterRoleBindingList{}
	g.Expect(cl.List(context.Background(), crbList)).To(Succeed())
	g.Expect(crbList.Items).To(BeEmpty())

	crList := &rbacv1.ClusterRoleList{}
	g.Expect(cl.List(context.Background(), crList)).To(Succeed())
	g.Expect(crList.Items).To(BeEmpty())
}

func TestCleanupClusterResourcesWaitsForFeastOperatorRelease(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	// Healthy ConfigMap claimed by both operators; the feast operator
	// finalizer signals it still owns resources requiring cleanup.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
			Finalizers: []string{
				CapabilitiesConfigMapFinalizer,
				feastOperatorCapabilitiesFinalizer,
			},
		},
		Data: map[string]string{
			capabilitiesKeyFeatureStoreEnabled: "true",
			capabilitiesKeyDataRegistryEnabled: "true",
		},
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "feast-crb",
			Labels: map[string]string{
				labels.ODH.Component(componentName): labels.True,
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm, crb).Build()

	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   false,
			DataRegistryEnabled:   false,
		},
	}

	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	// First finalizer run: teardown intent is written, the ConfigMap is
	// deleted (module finalizer kept), and the run defers with an error
	// while the feast operator still holds its finalizer.
	err := m.cleanupClusterResources(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("waiting for feast operator"))

	stored := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(cm), stored)).To(Succeed())
	g.Expect(stored.DeletionTimestamp).NotTo(BeNil())
	g.Expect(stored.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(stored.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("false"))
	g.Expect(stored.Finalizers).To(ContainElement(CapabilitiesConfigMapFinalizer))
	// The ClusterRoleBinding must still exist: cleanup has not completed.
	crbList := &rbacv1.ClusterRoleBindingList{}
	g.Expect(cl.List(context.Background(), crbList)).To(Succeed())
	g.Expect(crbList.Items).To(HaveLen(1))

	// Simulate the feast operator finishing its cleanup: it removes its
	// finalizer from the Terminating ConfigMap.
	controllerutil.RemoveFinalizer(stored, feastOperatorCapabilitiesFinalizer)
	g.Expect(cl.Update(context.Background(), stored)).To(Succeed())

	// Second finalizer run: the feast operator release is observed, the
	// module finalizer is removed, and the uninstall completes.
	g.Expect(m.cleanupClusterResources(context.Background(), rr)).To(Succeed())

	getErr := cl.Get(context.Background(), client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})
	g.Expect(k8serr.IsNotFound(getErr)).To(BeTrue())
	crbList = &rbacv1.ClusterRoleBindingList{}
	g.Expect(cl.List(context.Background(), crbList)).To(Succeed())
	g.Expect(crbList.Items).To(BeEmpty())
}

func TestCleanupClusterResourcesDisablesAlreadyTerminatingConfigMap(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	// Deletion started long before this finalizer. The flags are still enabled,
	// so the wait must be measured from the cleanup-start annotation rather
	// than this old deletionTimestamp.
	stale := metav1.Time{Time: metav1.Now().Add(-2 * feastOperatorReleaseTimeout)}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
			Finalizers: []string{
				CapabilitiesConfigMapFinalizer,
				feastOperatorCapabilitiesFinalizer,
			},
			DeletionTimestamp: &stale,
		},
		Data: map[string]string{
			capabilitiesKeyFeatureStoreEnabled: "true",
			capabilitiesKeyDataRegistryEnabled: "true",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm).Build()

	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   false,
			DataRegistryEnabled:   false,
		},
	}
	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	err := m.cleanupClusterResources(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("waiting for feast operator"))

	stored := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(cm), stored)).To(Succeed())
	g.Expect(stored.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(stored.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("false"))
	started, parseErr := time.Parse(time.RFC3339, stored.Annotations[capabilitiesCleanupStartedAnnotation])
	g.Expect(parseErr).NotTo(HaveOccurred())
	g.Expect(time.Since(started)).To(BeNumerically("<", time.Minute))
	g.Expect(stored.Finalizers).To(ConsistOf(
		CapabilitiesConfigMapFinalizer,
		feastOperatorCapabilitiesFinalizer,
	))

	// A retry while the flags are already disabled must not write the ConfigMap again.
	snapshot := stored.DeepCopy()
	err = m.cleanupClusterResources(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("waiting for feast operator"))
	g.Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(cm), stored)).To(Succeed())
	g.Expect(stored).To(Equal(snapshot))
}

func TestCleanupClusterResourcesTimesOutWaitingForFeastOperator(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(rbacv1.AddToScheme(scheme))

	feast := newTestFeastOperator()
	// Cleanup was recorded longer ago than the release timeout, and the feast
	// operator finalizer is still held. The deletionTimestamp is older still;
	// the timeout is the cleanup-start annotation.
	stale := metav1.Time{Time: metav1.Now().Add(-2 * feastOperatorReleaseTimeout)}
	startedAt := stale.UTC().Format(time.RFC3339)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
			Finalizers: []string{
				CapabilitiesConfigMapFinalizer,
				feastOperatorCapabilitiesFinalizer,
			},
			Annotations: map[string]string{
				capabilitiesCleanupStartedAnnotation: startedAt,
			},
			DeletionTimestamp: &stale,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm).Build()

	recorder := events.NewFakeRecorder(1)
	m := &Module{
		cfg: &moduleconfig.Config{
			ApplicationsNamespace: "test-ns",
			FeatureStoreEnabled:   false,
			DataRegistryEnabled:   false,
		},
		recorder: recorder,
	}

	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	// The timeout lets the uninstall proceed even though the feast operator
	// never released the ConfigMap.
	g.Expect(m.cleanupClusterResources(context.Background(), rr)).To(Succeed())

	// The ConfigMap is left Terminating, held only by the stuck feast
	// operator finalizer; the module finalizer is gone. Teardown intent is
	// still written so a late feast-operator reconcile can observe it.
	stored := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKeyFromObject(cm), stored)).To(Succeed())
	g.Expect(stored.Finalizers).To(ConsistOf(feastOperatorCapabilitiesFinalizer))
	g.Expect(stored.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(stored.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("false"))
	g.Expect(recorder.Events).To(Receive(And(
		ContainSubstring("Warning "+capabilitiesCleanupTimeoutReason),
		ContainSubstring(capabilitiesConfigMapName),
		ContainSubstring("test-ns"),
		ContainSubstring(feastOperatorCapabilitiesFinalizer),
		ContainSubstring(startedAt),
		ContainSubstring("remove only this finalizer"),
		Not(ContainSubstring("finalizers")),
	)))
}

func TestParseAndValidateOIDCIssuerURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{"valid https", "https://issuer.example.com", false, ""},
		{"valid https with path", "https://issuer.example.com/path", false, ""},
		{"http not allowed", "http://issuer.example.com", true, "https scheme"},
		{"no host", "https://", true, "host"},
		{"not a url", "not-a-url", true, ""},
		{"empty", "", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := parseAndValidateOIDCIssuerURL(tt.input)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
