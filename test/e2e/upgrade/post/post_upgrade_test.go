//go:build e2e

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

package post

import (
	"os"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

const (
	platformPartOf = "platform.opendatahub.io/part-of"
	feastPartOf    = "feastoperator"
)

// TestPostUpgradeModuleOperatorRunning verifies the feast-module-operator controller
// (feast-operator-controller-manager pod) is still running after the upgrade.
// This ensures the module operator that manages FeastOperator CRs survived the upgrade.
func TestPostUpgradeModuleOperatorRunning(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(func(g Gomega) {
		pods, err := k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "control-plane=controller-manager,app.kubernetes.io/name=feast-operator",
		})
		g.Expect(err).NotTo(HaveOccurred())

		var activePods []corev1.Pod
		for _, p := range pods.Items {
			if p.DeletionTimestamp == nil {
				activePods = append(activePods, p)
			}
		}
		g.Expect(activePods).To(HaveLen(1), "expected 1 module operator controller pod running")
		g.Expect(activePods[0].Name).To(ContainSubstring("feast-operator-controller-manager"))
		g.Expect(string(activePods[0].Status.Phase)).To(Equal("Running"))

		t.Logf("Module operator pod running: %s", activePods[0].Name)
	}).Should(Succeed())
}

// TestPostUpgradeFeastOperatorCRReady verifies the FeastOperator CR created in pre-upgrade
// tests is still in Ready state after the upgrade. This ensures CR persistence and reconciliation.
func TestPostUpgradeFeastOperatorCRReady(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(func(g Gomega) {
		feast, err := k8sClient.GetFeastOperator(ctx, componentsv1alpha1.FeastOperatorInstanceName)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(feast.Status.Phase).To(Equal("Ready"))
		g.Expect(feast.Status.ObservedGeneration).To(Equal(feast.Generation))

		var readyFound, provFound bool
		for i := range feast.Status.Conditions {
			c := &feast.Status.Conditions[i]
			switch c.Type {
			case "Ready":
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
				readyFound = true
			case "ProvisioningSucceeded":
				g.Expect(c.Status).To(Equal(metav1.ConditionTrue))
				provFound = true
			}
		}
		g.Expect(readyFound).To(BeTrue(), "Ready condition not found")
		g.Expect(provFound).To(BeTrue(), "ProvisioningSucceeded condition not found")

		t.Logf("FeastOperator CR is Ready: %s", feast.Name)
	}).Should(Succeed())
}

// TestPostUpgradeFeastDeploymentReady verifies the Feast operator deployment
// (opendatahub-feast-operator) created by the FeastOperator CR is still ready after upgrade.
// This is the actual Feast operator workload managed by the module operator.
func TestPostUpgradeFeastDeploymentReady(t *testing.T) {
	g := NewWithT(t)

	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty(), "Expected at least one Feast deployment")

	for _, d := range deployments.Items {
		g.Expect(d.Status.ReadyReplicas).To(Equal(d.Status.Replicas),
			"Deployment %s should have all replicas ready", d.Name)
		t.Logf("Feast deployment ready: %s (%d/%d replicas)", d.Name, d.Status.ReadyReplicas, d.Status.Replicas)
	}
}

// TestPostUpgradeVersionsUpdated verifies the operator versions in the FeastOperator CR status
// were updated to the new versions after the upgrade.
// Env vars FEAST_VERSION and FEAST_OPERATOR_PLATFORM_VERSION are required in CI to validate versions.
// For local testing without env vars, the test only verifies versions are populated.
func TestPostUpgradeVersionsUpdated(t *testing.T) {
	g := NewWithT(t)

	feast, err := k8sClient.GetFeastOperator(ctx, componentsv1alpha1.FeastOperatorInstanceName)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify releases are populated
	g.Expect(feast.Status.Releases).NotTo(BeEmpty(), "Post-upgrade releases should be set")

	expectedFeastVersion := os.Getenv("FEAST_VERSION")
	expectedPlatformVersion := os.Getenv("FEAST_OPERATOR_PLATFORM_VERSION")

	// In CI environments, version validation is mandatory
	if os.Getenv("CI") != "" {
		if expectedFeastVersion == "" || expectedPlatformVersion == "" {
			t.Fatal("FEAST_VERSION and FEAST_OPERATOR_PLATFORM_VERSION environment variables are required in CI")
		}
	}

	if len(feast.Status.Releases) > 0 {
		t.Logf("Post-upgrade releases: %d entries", len(feast.Status.Releases))
		for _, release := range feast.Status.Releases {
			t.Logf("  Release: name=%s, version=%s", release.Name, release.Version)
			g.Expect(release.Version).NotTo(BeEmpty(), "Release version should be populated")

			// Validate Feast version if env var is set
			if release.Name == "Feast" && expectedFeastVersion != "" {
				g.Expect(release.Version).To(Equal(expectedFeastVersion),
					"Post-upgrade Feast version should match FEAST_VERSION")
				t.Logf("✓ Verified Feast version: %s matches expected: %s", release.Version, expectedFeastVersion)
			}

			// Validate platform version if env var is set
			if release.Name == "platform" && expectedPlatformVersion != "" {
				g.Expect(release.Version).To(Equal(expectedPlatformVersion),
					"Post-upgrade platform version should match FEAST_OPERATOR_PLATFORM_VERSION")
				t.Logf("✓ Verified platform version: %s matches expected: %s", release.Version, expectedPlatformVersion)
			}
		}
	}
}

// TestPostUpgradeFeastDeploymentRolledOut verifies the Feast operator deployment
// (opendatahub-feast-operator) successfully rolled out after the upgrade with all replicas updated.
func TestPostUpgradeFeastDeploymentRolledOut(t *testing.T) {
	g := NewWithT(t)

	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty())

	for _, d := range deployments.Items {
		if len(d.Spec.Template.Spec.Containers) > 0 {
			image := d.Spec.Template.Spec.Containers[0].Image
			t.Logf("Post-upgrade Feast deployment %s image: %s", d.Name, image)
			g.Expect(image).NotTo(BeEmpty(), "Deployment image should be set")

			// Verify deployment rollout completed (all pods updated)
			g.Expect(d.Status.UpdatedReplicas).To(Equal(d.Status.Replicas),
				"Deployment %s should have all replicas updated", d.Name)
			t.Logf("✓ Feast deployment %s rolled out: %d/%d replicas updated",
				d.Name, d.Status.UpdatedReplicas, d.Status.Replicas)
		}
	}
}

// TestPostUpgradeTestDataSurvived verifies the test ConfigMap created in pre-upgrade tests
// still exists after the upgrade. This ensures data persistence through the upgrade.
// The ConfigMap is deleted at the end of the test to avoid leaving test data behind.
func TestPostUpgradeTestDataSurvived(t *testing.T) {
	g := NewWithT(t)

	cmName := "feast-upgrade-test-data"
	cm, err := k8sClient.CoreV1().ConfigMaps(support.DefaultApplicationsNamespace).Get(ctx, cmName, metav1.GetOptions{})
	g.Expect(err).NotTo(HaveOccurred(), "Test ConfigMap should still exist after upgrade")

	g.Expect(cm.Data).To(HaveKeyWithValue("test-key", "test-value"))
	g.Expect(cm.Data).To(HaveKeyWithValue("pre-upgrade", "true"))
	g.Expect(cm.Data).To(HaveKeyWithValue("test-message", "This ConfigMap should survive the upgrade"))

	t.Logf("✓ Test ConfigMap survived upgrade: %s/%s", cm.Namespace, cm.Name)

	t.Cleanup(func() {
		err := k8sClient.CoreV1().ConfigMaps(support.DefaultApplicationsNamespace).Delete(ctx, cmName, metav1.DeleteOptions{})
		if err != nil {
			t.Logf("Failed to clean up test ConfigMap %s/%s: %v", support.DefaultApplicationsNamespace, cmName, err)
			return
		}
		t.Logf("Cleaned up test ConfigMap: %s/%s", support.DefaultApplicationsNamespace, cmName)
	})
}

// TestPostUpgradeFeastDeploymentAnnotations verifies platform annotations are still present
// on the Feast operator deployment (opendatahub-feast-operator) after the upgrade.
// This ensures platform contract compliance.
func TestPostUpgradeFeastDeploymentAnnotations(t *testing.T) {
	g := NewWithT(t)

	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty())

	for _, d := range deployments.Items {
		annotations := d.GetAnnotations()
		g.Expect(annotations).To(HaveKey("platform.opendatahub.io/instance.name"),
			"Deployment %s should have instance.name annotation", d.Name)
		g.Expect(annotations).To(HaveKey("platform.opendatahub.io/instance.uid"),
			"Deployment %s should have instance.uid annotation", d.Name)
		g.Expect(annotations).To(HaveKey("platform.opendatahub.io/type"),
			"Deployment %s should have type annotation", d.Name)
		// Note: Version annotation is optional, just check other required annotations

		labels := d.GetLabels()
		g.Expect(labels).To(HaveKeyWithValue(platformPartOf, feastPartOf),
			"Deployment %s should have part-of label", d.Name)

		t.Logf("✓ Feast deployment %s platform annotations verified", d.Name)
	}
}

// TestPostUpgradeFeastDeploymentOwnerReferences verifies owner references are still set correctly
// on the Feast operator deployment (opendatahub-feast-operator) after the upgrade.
// This ensures garbage collection will work properly when the FeastOperator CR is deleted.
func TestPostUpgradeFeastDeploymentOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty())

	for _, d := range deployments.Items {
		ownerRefs := d.GetOwnerReferences()
		g.Expect(ownerRefs).NotTo(BeEmpty(),
			"Deployment %s should have owner references", d.Name)

		foundFeastOwner := false
		for _, ref := range ownerRefs {
			if ref.Kind == "FeastOperator" && ref.Name == componentsv1alpha1.FeastOperatorInstanceName {
				foundFeastOwner = true
				g.Expect(ref.Controller).NotTo(BeNil(), "Controller field should be set")
				if ref.Controller != nil {
					g.Expect(*ref.Controller).To(BeTrue(),
						"FeastOperator should be controller owner")
				}
				g.Expect(ref.BlockOwnerDeletion).NotTo(BeNil(), "BlockOwnerDeletion field should be set")
				if ref.BlockOwnerDeletion != nil {
					g.Expect(*ref.BlockOwnerDeletion).To(BeTrue(),
						"Owner deletion should be blocked")
				}
				t.Logf("✓ Feast deployment %s owned by FeastOperator CR: %s", d.Name, ref.Name)
				break
			}
		}
		g.Expect(foundFeastOwner).To(BeTrue(),
			"Deployment %s should have FeastOperator owner reference", d.Name)
	}
}
