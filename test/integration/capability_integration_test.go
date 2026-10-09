//go:build integration

package integration

import (
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/test/support"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

const (
	capabilitiesConfigMapName = "feast-capabilities-config"
	dataRegistryNamespaceName = "rhoai-data-registry"
)

type capabilityTests struct {
	*feastTest
}

func (ct *capabilityTests) Execute(t *testing.T) {
	t.Run("should project capabilities ConfigMap when toggles are enabled", ct.testCapabilitiesConfigMap)
	t.Run("should provision data registry namespace when enabled", ct.testDataRegistryNamespace)
}

func (ct *capabilityTests) testCapabilitiesConfigMap(t *testing.T) {
	g := NewWithT(t)
	testNamespace := support.IntegrationTestNamespace()

	cm := &corev1.ConfigMap{}
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{
			Name:      capabilitiesConfigMapName,
			Namespace: testNamespace,
		}, cm)).To(Succeed())
		g.Expect(cm.Data["featureStoreEnabled"]).To(Equal("true"))
		g.Expect(cm.Data["dataRegistryEnabled"]).To(Equal("true"))
		g.Expect(cm.Labels[labels.ODH.Component(componentsv1alpha1.FeastOperatorComponentName)]).To(Equal(labels.True))
		g.Expect(cm.OwnerReferences).To(HaveLen(1))
		g.Expect(cm.OwnerReferences[0].Kind).To(Equal("FeastOperator"))
		g.Expect(cm.Finalizers).To(ContainElement("components.platform.opendatahub.io/feast-capabilities"))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}

func (ct *capabilityTests) testDataRegistryNamespace(t *testing.T) {
	g := NewWithT(t)

	ns := &corev1.Namespace{}
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: dataRegistryNamespaceName}, ns)).To(Succeed())
		g.Expect(ns.Labels["dataregistry.opendatahub.io/enabled"]).To(Equal("true"))
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}
