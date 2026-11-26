//go:build integration

package integration

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

const dchLifecycleCapabilityLabel = "data.opendatahub.io/capability"

// testDataConnectHubRemoval exercises the running module controller and its
// real GC against the integration cluster. The test finalizer models a DCH
// application that has not finished cleanup; no database or live DCH service
// is needed to verify the module's lifecycle contract.
func (ct *capabilityTests) testDataConnectHubRemoval(t *testing.T) {
	g := NewWithT(t)
	const holdFinalizer = "test.opendatahub.io/dch-cleanup-hold"
	// Keep dc-controller inert so this test controls the simulated application
	// cleanup. Workload readiness and DCH's own finalizer have separate tests.
	for _, name := range []string{
		"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_CONTROLLER_IMAGE",
		"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_REST_IMAGE",
		"RELATED_IMAGE_ODH_DATA_CONNECT_HUB_FLIGHT_IMAGE",
		"RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE",
	} {
		t.Setenv(name, "example.invalid/dch-lifecycle:unused")
	}

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.module), ct.module)).To(Succeed())
	g.Expect(ct.module.Spec.DataConnectHubEnabled()).To(BeFalse(), "this test starts with DCH disabled")
	savedCapabilities := ct.module.DeepCopy().Spec.Capabilities
	moduleUID := ct.module.UID
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.workloadDeploy), ct.workloadDeploy)).To(Succeed())
	feastUID := ct.workloadDeploy.UID

	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "dch-lifecycle-"}}
	g.Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	application := &unstructured.Unstructured{}
	application.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "dataconnecthub.opendatahub.io", Version: "v1alpha1", Kind: "DataConnectService",
	})
	application.SetName("default-dataconnectservice")
	application.SetNamespace(namespace.Name)
	var heldUID types.UID
	// Release only our own finalizer before restoring the capability. Run this
	// even on failure, so test cleanup cannot strand the shared Data module.
	t.Cleanup(func() {
		if heldUID != "" {
			g.Expect(support.ReleaseFinalizer(ctx, k8sClient, application, heldUID, holdFinalizer)).To(Succeed())
		}
		g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			fresh := &componentsv1alpha1.FeastOperator{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.module), fresh); err != nil {
				return client.IgnoreNotFound(err)
			}
			fresh.Spec.Capabilities = savedCapabilities
			return k8sClient.Update(ctx, fresh)
		})).To(Succeed())
		g.Eventually(func() bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(application), application.DeepCopy())
			return k8serr.IsNotFound(err) || meta.IsNoMatchError(err)
		}).WithTimeout(timeout).WithPolling(interval).Should(BeTrue())
		g.Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, namespace))).To(Succeed())
	})
	// Administrator-owned data must survive capability removal.
	databaseConfig := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "dch-database-config", Namespace: namespace.Name},
		Data:       map[string][]byte{"secret-config.toml": []byte("test-database-configuration")},
	}
	g.Expect(k8sClient.Create(ctx, databaseConfig)).To(Succeed())
	databaseUID := databaseConfig.UID

	g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.module), ct.module); err != nil {
			return err
		}
		if ct.module.Spec.Capabilities == nil {
			ct.module.Spec.Capabilities = &componentsv1alpha1.CapabilitiesSpec{
				DataRegistry: componentsv1alpha1.CapabilitySpec{ManagementState: componentsv1alpha1.CapabilityManaged},
			}
		}
		ct.module.Spec.Capabilities.FeatureStore = componentsv1alpha1.CapabilitySpec{ManagementState: componentsv1alpha1.CapabilityManaged}
		ct.module.Spec.Capabilities.DataConnectHub = &componentsv1alpha1.DataConnectHubCapabilitySpec{
			CapabilitySpec: componentsv1alpha1.CapabilitySpec{ManagementState: componentsv1alpha1.CapabilityManaged},
			Namespace:      namespace.Name,
		}
		return k8sClient.Update(ctx, ct.module)
	})).To(Succeed())
	g.Eventually(func() error {
		return k8sClient.Get(ctx, client.ObjectKeyFromObject(application), application)
	}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	var err error
	heldUID, err = support.AddFinalizerToCurrentResource(ctx, k8sClient, application, holdFinalizer)
	g.Expect(err).NotTo(HaveOccurred())
	var installed []client.Object
	g.Eventually(func(g Gomega) {
		installed = nil
		for _, list := range dchLifecycleResourceLists() {
			g.Expect(k8sClient.List(ctx, list, client.MatchingLabels{dchLifecycleCapabilityLabel: "dataConnectHub"})).To(Succeed())
			items, err := meta.ExtractList(list)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(items).NotTo(BeEmpty(), "DCH controller bundle should contain %T", list)
			for _, item := range items {
				obj := item.(client.Object)
				g.Expect(obj.GetLabels()).To(HaveKeyWithValue(labelPartOf, "feastoperator"), "deploy must inject the GC selector label")
				g.Expect(metav1.GetControllerOf(obj)).NotTo(BeNil())
				g.Expect(metav1.GetControllerOf(obj).UID).To(Equal(moduleUID))
				installed = append(installed, obj)
			}
		}
	}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())

	g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.module), ct.module); err != nil {
			return err
		}
		ct.module.Spec.Capabilities.DataConnectHub.ManagementState = componentsv1alpha1.CapabilityRemoved
		return k8sClient.Update(ctx, ct.module)
	})).To(Succeed())
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(application), application)).To(Succeed())
		g.Expect(application.GetDeletionTimestamp()).NotTo(BeNil())
	}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	g.Consistently(func(g Gomega) {
		for _, obj := range installed {
			fresh := obj.DeepCopyObject().(client.Object)
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fresh)).To(Succeed())
			g.Expect(fresh.GetUID()).To(Equal(obj.GetUID()))
			g.Expect(fresh.GetDeletionTimestamp()).To(BeNil())
		}
	}).WithTimeout(10 * time.Second).WithPolling(interval).Should(Succeed())
	g.Expect(support.ReleaseFinalizer(ctx, k8sClient, application, heldUID, holdFinalizer)).To(Succeed())
	waitForDeleted(t, application)
	// Check actual cluster state after GC, not just an empty render result.
	g.Eventually(func(g Gomega) {
		for _, list := range dchLifecycleResourceLists() {
			g.Expect(k8sClient.List(ctx, list, client.MatchingLabels{dchLifecycleCapabilityLabel: "dataConnectHub"})).To(Succeed())
			items, err := meta.ExtractList(list)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(items).To(BeEmpty(), "DCH controller resources must be collected: %T", list)
		}
	}).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	config := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "opendatahub-dataconnecthub-config", Namespace: namespace.Name}}
	waitForDeleted(t, config)
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.module), ct.module)).To(Succeed())
	g.Expect(ct.module.UID).To(Equal(moduleUID))
	g.Expect(ct.module.Spec.Capabilities.FeatureStore.ManagementState).To(Equal(componentsv1alpha1.CapabilityManaged))
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ct.workloadDeploy), ct.workloadDeploy)).To(Succeed())
	g.Expect(ct.workloadDeploy.UID).To(Equal(feastUID))
	eventuallyDeploymentReady(t, ct.workloadDeploy)
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(namespace), namespace)).To(Succeed())
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(databaseConfig), databaseConfig)).To(Succeed())
	g.Expect(databaseConfig.UID).To(Equal(databaseUID))
	g.Expect(databaseConfig.Data["secret-config.toml"]).To(Equal([]byte("test-database-configuration")))
}

func dchLifecycleResourceLists() []client.ObjectList {
	return []client.ObjectList{
		&appsv1.DeploymentList{},
		&corev1.ServiceAccountList{},
		&rbacv1.RoleList{},
		&rbacv1.RoleBindingList{},
		&rbacv1.ClusterRoleList{},
		&rbacv1.ClusterRoleBindingList{},
	}
}
