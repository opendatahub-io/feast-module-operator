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

package pre

import (
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

// TestPreUpgradeControllerRunning verifies the feast-module-operator controller is running
// before the upgrade. This establishes the baseline state.
func TestPreUpgradeControllerRunning(t *testing.T) {
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
		g.Expect(activePods).To(HaveLen(1), "expected 1 feast-operator controller pod running")
		g.Expect(activePods[0].Name).To(ContainSubstring("feast-operator"))
		g.Expect(string(activePods[0].Status.Phase)).To(Equal("Running"))
	}).Should(Succeed())
}

// TestPreUpgradeFeastOperatorReady creates a FeastOperator CR and verifies it reaches Ready state.
// This CR will persist through the upgrade to test data preservation.
func TestPreUpgradeFeastOperatorReady(t *testing.T) {
	g := NewWithT(t)

	// Create FeastOperator CR (or verify it already exists)
	err := k8sClient.CreateFeastOperator(ctx, componentsv1alpha1.FeastOperatorInstanceName)
	if err != nil {
		// If it already exists, that's fine - just verify it's there
		_, getErr := k8sClient.GetFeastOperator(ctx, componentsv1alpha1.FeastOperatorInstanceName)
		g.Expect(getErr).NotTo(HaveOccurred(), "FeastOperator CR should exist")
		t.Logf("FeastOperator CR already exists, verifying its state")
	} else {
		t.Logf("Created FeastOperator CR: %s", componentsv1alpha1.FeastOperatorInstanceName)
	}

	// Verify FeastOperator reaches Ready state
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
	}).Should(Succeed())

	// Verify feast-operator deployment is ready
	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty(), "Expected at least one Feast deployment")
	for _, d := range deployments.Items {
		g.Expect(d.Status.ReadyReplicas).To(Equal(d.Status.Replicas),
			"Deployment %s should have all replicas ready", d.Name)
	}
}

// TestPreUpgradeVersionsPopulated verifies that version information is properly
// set in the FeastOperator CR status before the upgrade.
func TestPreUpgradeVersionsPopulated(t *testing.T) {
	g := NewWithT(t)

	feast, err := k8sClient.GetFeastOperator(ctx, componentsv1alpha1.FeastOperatorInstanceName)
	g.Expect(err).NotTo(HaveOccurred())

	// Note: Status.Releases is an array; we'll just check it's populated
	g.Expect(feast.Status.Releases).NotTo(BeEmpty(), "Releases should be set")

	if len(feast.Status.Releases) > 0 {
		t.Logf("Pre-upgrade releases: %d entries", len(feast.Status.Releases))
		for _, release := range feast.Status.Releases {
			t.Logf("  Release: name=%s, version=%s", release.Name, release.Version)
		}
	}
	t.Logf("Pre-upgrade UID: %s", feast.UID)

	// Verify deployment image is set
	deployments, err := k8sClient.AppsV1().Deployments(support.DefaultApplicationsNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: platformPartOf + "=" + feastPartOf,
	})
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(deployments.Items).NotTo(BeEmpty())

	for _, d := range deployments.Items {
		if len(d.Spec.Template.Spec.Containers) > 0 {
			image := d.Spec.Template.Spec.Containers[0].Image
			t.Logf("Pre-upgrade deployment %s image: %s", d.Name, image)
			g.Expect(image).NotTo(BeEmpty())
		}
	}
}

// TestPreUpgradeCreateTestData creates a configmap that will be checked after upgrade
// to verify data persistence through the upgrade process.
func TestPreUpgradeCreateTestData(t *testing.T) {
	g := NewWithT(t)

	cmName := "feast-upgrade-test-data"
	testCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: support.DefaultApplicationsNamespace,
			Labels: map[string]string{
				"test.opendatahub.io/upgrade": "true",
			},
		},
		Data: map[string]string{
			"test-key":     "test-value",
			"pre-upgrade":  "true",
			"test-message": "This ConfigMap should survive the upgrade",
		},
	}

	// Try to create, or update if already exists
	_, err := k8sClient.CoreV1().ConfigMaps(support.DefaultApplicationsNamespace).Create(ctx, testCM, metav1.CreateOptions{})
	if err != nil {
		// If it already exists, update it with fresh data
		existing, getErr := k8sClient.CoreV1().ConfigMaps(support.DefaultApplicationsNamespace).Get(ctx, cmName, metav1.GetOptions{})
		g.Expect(getErr).NotTo(HaveOccurred(), "Failed to get existing test ConfigMap")

		existing.Data = testCM.Data
		existing.Labels = testCM.Labels
		_, updateErr := k8sClient.CoreV1().ConfigMaps(support.DefaultApplicationsNamespace).Update(ctx, existing, metav1.UpdateOptions{})
		g.Expect(updateErr).NotTo(HaveOccurred(), "Failed to update test ConfigMap")
		t.Logf("Updated existing test ConfigMap: %s/%s", support.DefaultApplicationsNamespace, cmName)
	} else {
		t.Logf("Created test ConfigMap: %s/%s", support.DefaultApplicationsNamespace, cmName)
	}
}
