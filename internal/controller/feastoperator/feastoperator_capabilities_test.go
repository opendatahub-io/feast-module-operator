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
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

func initCapabilitiesTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	return scheme
}

func newCapabilitiesTestModule(t *testing.T, featureStoreEnabled, dataRegistryEnabled bool) *Module {
	t.Helper()

	repoRoot := filepath.Join("..", "..", "..")
	cfg := &moduleconfig.Config{
		PlatformName:          string(cluster.OpenDataHub),
		PlatformVersion:       "1.0.0",
		ManifestsPath:         filepath.Join(repoRoot, "config", "manifests"),
		ApplicationsNamespace: "test-ns",
		FeatureStoreEnabled:   featureStoreEnabled,
		DataRegistryEnabled:   dataRegistryEnabled,
	}

	m, err := NewModule(cfg)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return m
}

func newCapabilitiesRR(t *testing.T, cl client.Client, obj *componentApi.FeastOperator, m *Module) *odhtypes.ReconciliationRequest {
	t.Helper()
	g := NewWithT(t)

	repoRoot := filepath.Join("..", "..", "..")
	rr := &odhtypes.ReconciliationRequest{
		Instance:          obj,
		Client:            cl,
		ManifestsBasePath: filepath.Join(repoRoot, "config", "manifests"),
		Release: (&moduleconfig.Config{
			PlatformName:    string(cluster.OpenDataHub),
			PlatformVersion: "1.0.0",
		}).Release(),
	}
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	return rr
}

func TestReconcileCapabilitiesConfigMapCreatesConfigMap(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("false"))
	g.Expect(cm.Labels[labels.ODH.Component(componentName)]).To(Equal(labels.True))
	g.Expect(cm.OwnerReferences).To(HaveLen(1))
	g.Expect(cm.OwnerReferences[0].Name).To(Equal(componentApi.FeastOperatorInstanceName))
	g.Expect(cm.Finalizers).To(ContainElement(capabilitiesConfigMapFinalizer))
}

func TestReconcileCapabilitiesConfigMapUpdatesExistingConfigMap(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
		},
		Data: map[string]string{
			capabilitiesKeyFeatureStoreEnabled: "true",
			capabilitiesKeyDataRegistryEnabled: "true",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, existing).Build()

	m := newCapabilitiesTestModule(t, false, true)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("true"))
	g.Expect(cm.Finalizers).To(ContainElement(capabilitiesConfigMapFinalizer))
}

func TestReconcileCapabilitiesConfigMapRecreatesDeletedConfigMap(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()
	m := newCapabilitiesTestModule(t, true, true)
	rr := newCapabilitiesRR(t, cl, feast, m)
	g.Expect(m.reconcileCapabilitiesConfigMap(ctx, rr)).To(Succeed())

	key := client.ObjectKey{Name: capabilitiesConfigMapName, Namespace: "test-ns"}
	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(ctx, key, cm)).To(Succeed())
	g.Expect(cl.Delete(ctx, cm)).To(Succeed())
	g.Expect(m.reconcileCapabilitiesConfigMap(ctx, rr)).NotTo(Succeed())
	g.Expect(m.reconcileCapabilitiesConfigMap(ctx, rr)).To(Succeed())
	g.Expect(cl.Get(ctx, key, cm)).To(Succeed())
	g.Expect(cm.Finalizers).To(ContainElement(capabilitiesConfigMapFinalizer))
}

func TestReconcileCapabilitiesFromSpecOverridesEnv(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityRemoved},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	// Env says both true — but spec.capabilities overrides
	m := newCapabilitiesTestModule(t, true, true)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryNS]).To(Equal("rhoai-data-registry"))
}

func TestReconcileCapabilitiesFromSpecDefaultNamespace(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, false, false)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryNS]).To(Equal("rhoai-data-registry"))
}

func TestReconcileCapabilitiesFallbackToEnvWhenSpecAbsent(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	// No spec.capabilities set — should fall back to env config
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, false, true)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("false"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryNS]).To(Equal("rhoai-data-registry"))
}

func TestReconcileCapabilitiesNoNamespaceKeyWhenDRDisabled(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesTestScheme()
	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityRemoved},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, true, true)
	rr := newCapabilitiesRR(t, cl, feast, m)

	g.Expect(m.reconcileCapabilitiesConfigMap(context.Background(), rr)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      capabilitiesConfigMapName,
		Namespace: "test-ns",
	}, cm)).To(Succeed())
	g.Expect(cm.Data[capabilitiesKeyFeatureStoreEnabled]).To(Equal("true"))
	g.Expect(cm.Data[capabilitiesKeyDataRegistryEnabled]).To(Equal("false"))
	_, hasNSKey := cm.Data[capabilitiesKeyDataRegistryNS]
	g.Expect(hasNSKey).To(BeFalse(), "dataRegistryNamespace should not be set when DR is disabled")
}

func TestCapabilitiesDataHash(t *testing.T) {
	g := NewWithT(t)

	h1 := capabilitiesDataHash(map[string]string{
		"featureStoreEnabled": "true",
		"dataRegistryEnabled": "false",
	})
	h2 := capabilitiesDataHash(map[string]string{
		"dataRegistryEnabled": "false",
		"featureStoreEnabled": "true",
	})
	g.Expect(h1).To(Equal(h2), "hash should be order-independent")
	g.Expect(h1).To(HaveLen(16))

	h3 := capabilitiesDataHash(map[string]string{
		"featureStoreEnabled": "true",
		"dataRegistryEnabled": "true",
	})
	g.Expect(h3).NotTo(Equal(h1), "different data should produce different hash")
}

func initCapabilitiesRolloutTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	utilruntime.Must(appsv1.SchemeBuilder.AddToScheme(scheme))
	return scheme
}

func TestTriggerCapabilityRolloutAnnotatesDeployment(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesRolloutTestScheme()
	feast := newTestFeastOperator()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
		},
		Data: map[string]string{
			capabilitiesKeyFeatureStoreEnabled: "true",
			capabilitiesKeyDataRegistryEnabled: "false",
		},
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "feast"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "feast"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "manager",
						Image: "test:latest",
					}},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm, deploy).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.triggerCapabilityRolloutIfNeeded(context.Background(), rr)).To(Succeed())

	updated := &appsv1.Deployment{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      deploymentName,
		Namespace: "test-ns",
	}, updated)).To(Succeed())
	g.Expect(updated.Spec.Template.Annotations).To(HaveKey(capabilitiesHashAnnotation))
	g.Expect(updated.Spec.Template.Annotations[capabilitiesHashAnnotation]).To(HaveLen(16))
}

func TestTriggerCapabilityRolloutSkipsWhenHashUnchanged(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesRolloutTestScheme()
	feast := newTestFeastOperator()
	cmData := map[string]string{
		capabilitiesKeyFeatureStoreEnabled: "true",
		capabilitiesKeyDataRegistryEnabled: "false",
	}
	hash := capabilitiesDataHash(cmData)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
		},
		Data: cmData,
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "feast"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      map[string]string{"app": "feast"},
					Annotations: map[string]string{capabilitiesHashAnnotation: hash},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "manager",
						Image: "test:latest",
					}},
				},
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm, deploy).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.triggerCapabilityRolloutIfNeeded(context.Background(), rr)).To(Succeed())

	updated := &appsv1.Deployment{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{
		Name:      deploymentName,
		Namespace: "test-ns",
	}, updated)).To(Succeed())
	g.Expect(updated.Spec.Template.Annotations[capabilitiesHashAnnotation]).To(Equal(hash))
}

func TestTriggerCapabilityRolloutSkipsWhenNoDeployment(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesRolloutTestScheme()
	feast := newTestFeastOperator()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      capabilitiesConfigMapName,
			Namespace: "test-ns",
		},
		Data: map[string]string{
			capabilitiesKeyFeatureStoreEnabled: "true",
			capabilitiesKeyDataRegistryEnabled: "false",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, cm).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.triggerCapabilityRolloutIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestTriggerCapabilityRolloutSkipsWhenNoConfigMap(t *testing.T) {
	g := NewWithT(t)

	scheme := initCapabilitiesRolloutTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := &odhtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.triggerCapabilityRolloutIfNeeded(context.Background(), rr)).To(Succeed())
}
