//go:build integration

package integration

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	feastoperatorcontroller "github.com/opendatahub-io/feast-module-operator/internal/controller/feastoperator"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

type foundationTests struct {
	*feastTest
}

func (ft *foundationTests) Execute(t *testing.T) {
	t.Run("should have module CRD installed", ft.testModuleCRDInstalled)
	t.Run("should become ready", ft.testBecomesReady)
	t.Run("should deploy the manifests matching the platform type", ft.testPlatformManifests)
	t.Run("should report release version and platform", ft.testReleaseStatus)
	t.Run("should set platform labels and annotations", ft.testPlatformLabels)
	t.Run("should set owner references", ft.testOwnerReferences)
}

func (ft *foundationTests) testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.moduleCRD)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Matchf(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func (ft *foundationTests) testBecomesReady(t *testing.T) {
	g := NewWithT(t)

	ft.module.ResourceVersion = ""
	g.Expect(k8sClient.Create(ctx, ft.module)).To(Succeed())

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
	))

	eventuallyDeploymentReady(t, ft.workloadDeploy)
}

func (ft *foundationTests) testPlatformManifests(t *testing.T) {
	g := NewWithT(t)

	// RELATED_IMAGE_CRON_JOB is not part of the operator's image param map,
	// so the value on the deployed workload is exactly the one bundled in
	// the selected overlay's params.env.
	expected := support.OverlayParam(t, feastoperatorcontroller.OverlayFor(moduleconfig.ParsePlatformType(operatorPlatformType)), "RELATED_IMAGE_CRON_JOB")

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Matchf(`.spec.template.spec.containers[] | select(.name == "manager") | .env[] | select(.name == "RELATED_IMAGE_CRON_JOB") | .value == "%s"`, expected),
	)
}

func (ft *foundationTests) testReleaseStatus(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.releases | length > 0`),
		jq.Match(`.status.releases[] | select(.name == "Feast") | .version != ""`),
		jq.Matchf(`.status.releases[] | select(.name == "platform") | .version == "%s"`,
			testPlatformVersion),
	))
}

func (ft *foundationTests) testPlatformLabels(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Matchf(`.metadata.labels."%s" == "feastoperator"`, labelPartOf),
		jq.Matchf(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			ft.module.GetName()),
		jq.Matchf(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(ft.module.GetUID())),
		jq.Matchf(`.metadata.annotations."%s" == "%s"`,
			annotationType,
			operatorPlatformType),
		jq.Matchf(`.metadata.annotations."%s" == "%s"`,
			annotationVersion,
			operatorReleaseVersion),
	))
}

func (ft *foundationTests) testOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Matchf(`.metadata.ownerReferences[] | select(.kind == "FeastOperator") | .name == "%s"`,
			componentsv1alpha1.FeastOperatorInstanceName),
	)
}
