//go:build integration

package integration

import (
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	feastoperatorcontroller "github.com/opendatahub-io/feast-module-operator/internal/controller/feastoperator"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

type foundationTests struct {
	*feastTest
}

func (ft *foundationTests) Execute(t *testing.T) {
	t.Run("should have module CRD installed", ft.testModuleCRDInstalled)
	t.Run("should migrate the legacy operand selector and become ready", ft.testBecomesReady)
	t.Run("should deploy the manifests matching the platform type", ft.testPlatformManifests)
	t.Run("should report release version and platform", ft.testReleaseStatus)
	t.Run("should set platform labels and annotations", ft.testPlatformLabels)
	t.Run("should set owner references", ft.testOwnerReferences)
	t.Run("should preserve the operand on an equivalent capability update", ft.testPreservesOperand)
}

func (ft *foundationTests) testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.moduleCRD)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func (ft *foundationTests) testBecomesReady(t *testing.T) {
	g := NewWithT(t)
	// The module, not the ODH platform, owns the immutable-selector migration.
	// Seed the same legacy in-tree shape before creating its module CR.
	legacy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: ft.workloadDeploy.Name, Namespace: ft.workloadDeploy.Namespace,
			Labels: map[string]string{"app.opendatahub.io/feast-operator": "true", "app.kubernetes.io/part-of": "feast-operator"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(int32(1)),
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"control-plane": "controller-manager"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"control-plane": "controller-manager"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "manager", Image: "registry.k8s.io/pause:3.9", Command: []string{"/pause"}}}},
			},
		},
	}
	g.Expect(k8sClient.Create(ctx, legacy)).To(Succeed())

	ft.module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, ft.module)).To(Succeed())

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
	))

	eventuallyDeploymentReady(t, ft.workloadDeploy)
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.workloadDeploy), ft.workloadDeploy)).To(Succeed())
	g.Expect(ft.workloadDeploy.UID).NotTo(Equal(legacy.UID), "immutable selector migration must recreate the legacy Deployment")
	g.Expect(ft.workloadDeploy.Spec.Selector.MatchLabels).To(HaveKeyWithValue("app.kubernetes.io/name", "feast-operator"))
}

func (ft *foundationTests) testPreservesOperand(t *testing.T) {
	g := NewWithT(t)
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.workloadDeploy), ft.workloadDeploy)).To(Succeed())
	operandUID := ft.workloadDeploy.UID
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.module), ft.module)).To(Succeed())
	saved := ft.module.DeepCopy().Spec.Capabilities
	t.Cleanup(func() {
		g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.module), ft.module); err != nil {
				return err
			}
			ft.module.Spec.Capabilities = saved
			return k8sClient.Update(ctx, ft.module)
		})).To(Succeed())
	})
	// Replace the legacy environment defaults with the same effective CR
	// capabilities. A normal reconcile must adopt/update, not recreate, the
	// already-current operand.
	g.Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.module), ft.module); err != nil {
			return err
		}
		ft.module.Spec.Capabilities = &componentsv1alpha1.CapabilitiesSpec{
			FeatureStore: componentsv1alpha1.CapabilitySpec{ManagementState: componentsv1alpha1.CapabilityManaged},
			DataRegistry: componentsv1alpha1.CapabilitySpec{ManagementState: componentsv1alpha1.CapabilityManaged},
		}
		return k8sClient.Update(ctx, ft.module)
	})).To(Succeed())
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.module), ft.module)).To(Succeed())
		g.Expect(ft.module.Status.ObservedGeneration).To(Equal(ft.module.Generation))
		g.Expect(string(ft.module.Status.Phase)).To(Equal("Ready"))
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.workloadDeploy), ft.workloadDeploy)).To(Succeed())
		g.Expect(ft.workloadDeploy.UID).To(Equal(operandUID))
		owner := metav1.GetControllerOf(ft.workloadDeploy)
		g.Expect(owner).NotTo(BeNil())
		g.Expect(owner.UID).To(Equal(ft.module.UID))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func (ft *foundationTests) testPlatformManifests(t *testing.T) {
	g := NewWithT(t)

	// RELATED_IMAGE_CRON_JOB is not part of the operator's image param map,
	// so the value on the deployed workload is exactly the one bundled in
	// the selected overlay's params.env.
	expected := support.OverlayParam(t, feastoperatorcontroller.OverlayFor(operatorPlatformType), "RELATED_IMAGE_CRON_JOB")

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.spec.template.spec.containers[] | select(.name == "manager") | .env[] | select(.name == "RELATED_IMAGE_CRON_JOB") | .value == "%s"`, expected),
	)
}

func (ft *foundationTests) testReleaseStatus(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.releases | length > 0`),
		jq.Match(`.status.releases[] | select(.name == "Feast") | .version != ""`),
		jq.Match(`.status.releases[] | select(.name == "platform") | .version == "%s"`,
			testPlatformVersion),
	))
}

func (ft *foundationTests) testPlatformLabels(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.labels."%s" == "feastoperator"`, labelPartOf),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			ft.module.GetName()),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(ft.module.GetUID())),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationType,
			operatorPlatformType),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationVersion,
			operatorReleaseVersion),
	))
}

func (ft *foundationTests) testOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "FeastOperator") | .name == "%s"`,
			componentsv1alpha1.FeastOperatorInstanceName),
	)
}
