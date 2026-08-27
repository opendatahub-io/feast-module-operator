//go:build e2e

package e2e

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
)

type foundationTests struct {
	*feastE2ETest
	moduleCRD       *apiextensionsv1.CustomResourceDefinition
	operatorDeploy  *appsv1.Deployment
	operatorCfgMap  *corev1.ConfigMap
	workloadDeploy  *appsv1.Deployment
	workloadService *corev1.Service
}

func newFoundationTests(suite *feastE2ETest) *foundationTests {
	return &foundationTests{
		feastE2ETest: suite,
		moduleCRD: &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
		},
		operatorDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operatorDeployName,
				Namespace: suite.operatorNamespace,
			},
		},
		operatorCfgMap: &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      operatorConfigMapName,
				Namespace: suite.operatorNamespace,
			},
		},
		workloadDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      workloadDeployName,
				Namespace: suite.operatorNamespace,
			},
		},
		workloadService: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      workloadMetricsServiceName,
				Namespace: suite.operatorNamespace,
			},
		},
	}
}

func (ft *foundationTests) Execute(t *testing.T) {
	t.Run("should have module CRD installed", ft.testModuleCRDInstalled)
	t.Run("should have operator ConfigMap deployed", ft.testOperatorConfigMap)
	t.Run("should reject a non-singleton FeastOperator", ft.testSingletonEnforced)
	t.Run("should become ready", ft.testBecomesReady)
	t.Run("should have operator and operand Deployments available", ft.testDeploymentsAvailable)
	t.Run("should report release version and platform", ft.testReleaseStatus)
	t.Run("should set platform labels and annotations", ft.testPlatformLabels)
	t.Run("should set owner references", ft.testOwnerReferences)
	t.Run("should expose a reachable operand Service", ft.testOperandServiceReachable)
	t.Run("should reflect spec changes in operands", ft.testSpecProjectedToOperand)
	t.Run("should report not ready when a critical operand fails", ft.testReadyFalseOnOperandFailure)
	t.Run("should remove operands when the module CR is deleted", ft.testCleanupRemovesOperands)
}

func (ft *foundationTests) testModuleCRDInstalled(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.moduleCRD)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.name == "%s"`, moduleCRDName),
	)
}

func (ft *foundationTests) testOperatorConfigMap(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.operatorCfgMap)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformName),
		jq.Match(`.data."%s" != ""`, moduleconfig.KeyPlatformVersion),
	))
}

func (ft *foundationTests) testBecomesReady(t *testing.T) {
	g := NewWithT(t)

	if ft.createdByTest {
		ft.module.ResourceVersion = ""
		g.Expect(k8sClient.Create(ctx, ft.module)).To(Succeed())
	} else {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(ft.module), ft.module)).To(Succeed())
	}

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "ProvisioningSucceeded") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "DeploymentsAvailable") | .status == "True"`),
	))

	eventuallyDeploymentReady(t, ft.workloadDeploy)
}

func (ft *foundationTests) testDeploymentsAvailable(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.operatorDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.namespace == "%s"`, ft.operatorNamespace),
		jq.Match(`.status.readyReplicas >= 1`),
		jq.Match(`.metadata.labels."%s" == "%s"`, labelAppName, operatorDeployName),
		jq.Match(`.metadata.labels."%s" == "platform"`, labelPartOf),
	))

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.namespace == "%s"`, ft.operatorNamespace),
		jq.Match(`.status.readyReplicas >= 1`),
		jq.Match(`.metadata.labels."%s" == "true"`, odhComponentLabel),
		jq.Match(`.metadata.labels."%s" == "feast-operator"`, labelAppName),
		jq.Match(`.metadata.labels."%s" == "feastoperator"`, labelK8sPartOf),
		jq.Match(`.metadata.labels."%s" == "feastoperator"`, labelPartOf),
		jq.Match(`.metadata.labels."%s" == "controller-manager"`, labelControlPlane),
	))

	deploys := &appsv1.DeploymentList{}
	g.Expect(k8sClient.List(ctx, deploys,
		client.InNamespace(ft.operatorNamespace),
		client.MatchingLabels{odhComponentLabel: "true"},
	)).To(Succeed())
	g.Expect(deploys.Items).NotTo(BeEmpty(),
		"expected feast operand Deployments labeled %s=true in %s", odhComponentLabel, ft.operatorNamespace)
	for i := range deploys.Items {
		g.Expect(deploys.Items[i].Status.ReadyReplicas).To(BeNumerically(">=", 1),
			"Deployment %s should have ready replicas", deploys.Items[i].Name)
	}
}

func (ft *foundationTests) testReleaseStatus(t *testing.T) {
	g := NewWithT(t)

	module := ft.module.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module)).To(Succeed())

	expectedPlatformVersion := module.GetAnnotations()[annotationVersion]
	g.Expect(expectedPlatformVersion).NotTo(BeEmpty(),
		"FeastOperator should have %s annotation from the platform", annotationVersion)
	g.Expect(expectedPlatformVersion).NotTo(Equal("unknown"))

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.releases | length > 0`),
		jq.Match(`.status.releases[] | select(.name == "Feast") | .version != ""`),
		jq.Match(`.status.releases[] | select(.name == "platform") | .version == "%s"`,
			expectedPlatformVersion),
	))
}

func (ft *foundationTests) testPlatformLabels(t *testing.T) {
	g := NewWithT(t)
	module := ft.module.DeepCopy()

	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module)).To(Succeed())

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.metadata.labels."%s" == "feastoperator"`, labelPartOf),
		jq.Match(`.metadata.labels."%s" == "true"`, odhComponentLabel),
		jq.Match(`.metadata.labels."%s" == "feast-operator"`, labelAppName),
		jq.Match(`.metadata.labels."%s" == "feastoperator"`, labelK8sPartOf),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceName,
			module.GetName()),
		jq.Match(`.metadata.annotations."%s" == "%s"`,
			annotationInstanceUID,
			string(module.GetUID())),
		jq.Match(`.metadata.annotations."%s" != null and .metadata.annotations."%s" != ""`,
			annotationType, annotationType),
		jq.Match(`.metadata.annotations."%s" != null and .metadata.annotations."%s" != "" and .metadata.annotations."%s" != "unknown"`,
			annotationVersion, annotationVersion, annotationVersion),
	))
}

func (ft *foundationTests) testOwnerReferences(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.metadata.ownerReferences[] | select(.kind == "FeastOperator") | .name == "%s"`,
			componentsv1alpha1.FeastOperatorInstanceName),
	)
}

func (ft *foundationTests) testSingletonEnforced(t *testing.T) {
	g := NewWithT(t)

	invalid := &componentsv1alpha1.FeastOperator{
		ObjectMeta: metav1.ObjectMeta{Name: "not-the-singleton"},
	}

	err := k8sClient.Create(ctx, invalid)
	g.Expect(err).To(HaveOccurred())
	g.Expect(k8serr.IsInvalid(err) || k8serr.IsForbidden(err)).To(BeTrue(),
		"expected CEL singleton validation to reject %s: %v", invalid.Name, err)
	g.Expect(err.Error()).To(ContainSubstring(componentsv1alpha1.FeastOperatorInstanceName))
}

func (ft *foundationTests) testOperandServiceReachable(t *testing.T) {
	g := NewWithT(t)

	g.Eventually(k.Get(ft.workloadService)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.spec.clusterIP != null and .spec.clusterIP != "" and .spec.clusterIP != "None"`),
		jq.Match(`.spec.ports | length > 0`),
	))

	g.Eventually(func(g Gomega) {
		slices := &discoveryv1.EndpointSliceList{}
		g.Expect(k8sClient.List(ctx, slices,
			client.InNamespace(ft.workloadService.Namespace),
			client.MatchingLabels{"kubernetes.io/service-name": ft.workloadService.Name},
		)).To(Succeed())
		g.Expect(slices.Items).NotTo(BeEmpty(),
			"expected EndpointSlices for Service %s/%s", ft.workloadService.Namespace, ft.workloadService.Name)

		ready := 0
		for i := range slices.Items {
			for _, ep := range slices.Items[i].Endpoints {
				if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
					ready++
				}
			}
		}
		g.Expect(ready).To(BeNumerically(">", 0),
			"expected at least one ready endpoint for Service %s/%s", ft.workloadService.Namespace, ft.workloadService.Name)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func (ft *foundationTests) testSpecProjectedToOperand(t *testing.T) {
	g := NewWithT(t)

	current := ft.module.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(current), current)).To(Succeed())
	var originalOIDC *common.GatewayOIDCSpec
	if current.Spec.OIDC != nil {
		originalOIDC = current.Spec.OIDC.DeepCopy()
	}

	t.Cleanup(func() {
		latest := ft.module.DeepCopy()
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(latest), latest); err != nil {
			return
		}
		restored := latest.DeepCopy()
		restored.Spec.OIDC = originalOIDC
		_ = k8sClient.Patch(ctx, restored, client.MergeFrom(latest))
	})

	patched := current.DeepCopy()
	patched.Spec.OIDC = &common.GatewayOIDCSpec{IssuerURL: testOIDCIssuerURL}
	g.Expect(k8sClient.Patch(ctx, patched, client.MergeFrom(current))).To(Succeed())

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.spec.oidc.issuerURL == "%s"`, testOIDCIssuerURL),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
	))

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.spec.template.spec.containers[] | select(.name == "manager") | .env[] | select(.name == "%s") | .value == "%s"`,
			oidcIssuerEnvName, testOIDCIssuerURL),
	)

	// Platform-managed spec field must not be overwritten on subsequent reconciles.
	g.Consistently(k.Get(ft.module)).WithContext(ctx).WithTimeout(10 * time.Second).WithPolling(interval).Should(
		jq.Match(`.spec.oidc.issuerURL == "%s"`, testOIDCIssuerURL),
	)
}

func (ft *foundationTests) testReadyFalseOnOperandFailure(t *testing.T) {
	g := NewWithT(t)

	g.Expect(k8sClient.Delete(ctx, ft.workloadDeploy.DeepCopy())).To(Succeed())

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(500 * time.Millisecond).Should(And(
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "False"`),
		jq.Match(`.status.conditions[] | select(.type == "DeploymentsAvailable") | .status == "False"`),
	))

	eventuallyDeploymentReady(t, ft.workloadDeploy)
	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
		jq.Match(`.status.conditions[] | select(.type == "DeploymentsAvailable") | .status == "True"`),
	))
}

func (ft *foundationTests) testCleanupRemovesOperands(t *testing.T) {
	if !ft.createdByTest {
		t.Skip("FeastOperator is platform-managed; deleting it would be reverted by the platform operator")
	}

	g := NewWithT(t)

	componentLabels := client.MatchingLabels{odhComponentLabel: "true"}

	clusterRoles := &rbacv1.ClusterRoleList{}
	g.Expect(k8sClient.List(ctx, clusterRoles, componentLabels)).To(Succeed())
	g.Expect(clusterRoles.Items).NotTo(BeEmpty(),
		"expected operand ClusterRoles labeled %s=true before cleanup", odhComponentLabel)

	clusterRoleBindings := &rbacv1.ClusterRoleBindingList{}
	g.Expect(k8sClient.List(ctx, clusterRoleBindings, componentLabels)).To(Succeed())
	g.Expect(clusterRoleBindings.Items).NotTo(BeEmpty(),
		"expected operand ClusterRoleBindings labeled %s=true before cleanup", odhComponentLabel)

	g.Expect(k8sClient.Delete(ctx, ft.module)).To(Succeed())

	waitForDeleted(t, ft.workloadDeploy)
	waitForDeleted(t, ft.workloadService)
	waitForLabeledResourcesGone(t, &rbacv1.ClusterRoleList{}, componentLabels)
	waitForLabeledResourcesGone(t, &rbacv1.ClusterRoleBindingList{}, componentLabels)
	waitForSingletonDeleted(t, ft.module)
}
