package support

import (
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestVerifyContainerImage(t *testing.T) {
	deployment := &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "manager", Image: "localhost:5001/konflux-sim/feast-module-operator@sha256:abc123"},
					},
				},
			},
		},
	}
	deployment.Name = "operator"

	g := NewWithT(t)
	g.Expect(VerifyContainerImage(deployment, "manager", "localhost:5001/konflux-sim/feast-module-operator@sha256:abc123")).To(Succeed())
	g.Expect(VerifyContainerImage(deployment, "manager", "quay.io/opendatahub/odh-feast-module-operator:odh-stable")).NotTo(Succeed())
	g.Expect(VerifyContainerImage(deployment, "missing", "unused")).NotTo(Succeed())
}
