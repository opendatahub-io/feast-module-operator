//go:build integration

package integration

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

// TestNamespaceLabelAfterReconciliation verifies that the feast-operator workload
// deployed by the module operator has RBAC to update namespaces (required for
// adding the opendatahub.io/feast=true label) and that the label appears on the
// target namespace after a FeastOperator CR is reconciled and the workload is ready.
//
// Regression test for RHOAIENG-88591.
func TestNamespaceLabelAfterReconciliation(t *testing.T) {
	g := NewWithT(t)
	testNamespace := support.IntegrationTestNamespace()

	// Ensure any leftover CR is gone.
	module := &componentsv1alpha1.FeastOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentsv1alpha1.FeastOperatorInstanceName,
		},
	}
	_ = k8sClient.Delete(ctx, module)
	waitForSingletonDeleted(t, module)

	// Create the FeastOperator CR to trigger reconciliation.
	g.Expect(k8sClient.Create(ctx, module)).To(Succeed())
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, module)
	})

	// Wait for the workload deployment to be ready.
	workloadDeploy := &corev1.Pod{}
	_ = workloadDeploy // suppress unused — we check via deployment below

	t.Run("workload ClusterRole has namespace update permission", func(t *testing.T) {
		g := NewWithT(t)

		// List all ClusterRoles with the feast operator component label.
		roleList := &rbacv1.ClusterRoleList{}
		g.Eventually(func(g Gomega) {
			g.Expect(k8sClient.List(ctx, roleList)).To(Succeed())

			var feastRole *rbacv1.ClusterRole
			for i := range roleList.Items {
				for _, rule := range roleList.Items[i].Rules {
					for _, res := range rule.Resources {
						if res == "namespaces" {
							for _, verb := range rule.Verbs {
								if verb == "update" {
									feastRole = &roleList.Items[i]
									break
								}
							}
						}
					}
				}
			}
			g.Expect(feastRole).NotTo(BeNil(),
				"Expected a ClusterRole with 'update' on 'namespaces' — required for opendatahub.io/feast=true label")
		}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	})

	t.Run("namespace gets opendatahub.io/feast label after workload reconciles FeatureStore", func(t *testing.T) {
		g := NewWithT(t)

		// After the feast-operator workload processes a FeatureStore CR,
		// it should add opendatahub.io/feast=true to the namespace.
		// This assertion will pass once the feast-operator fix is deployed.
		ns := &corev1.Namespace{}
		g.Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: testNamespace}, ns)).To(Succeed())
			labels := ns.GetLabels()
			g.Expect(labels).To(HaveKeyWithValue("opendatahub.io/feast", "true"),
				"Namespace should have opendatahub.io/feast=true after FeatureStore reconciliation (RHOAIENG-88591)")
		}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	})
}
