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
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
)

func initNamespaceTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentApi.AddToScheme(scheme))
	return scheme
}

func TestReconcileDataRegistryNamespaceCreatesNamespace(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, true, true)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Name: dataRegistryNamespaceName}, ns)).To(Succeed())
	g.Expect(ns.Labels[dataRegistryEnabledLabelKey]).To(Equal(dataRegistryEnabledLabelValue))
	g.Expect(ns.OwnerReferences).To(BeEmpty())
}

func TestReconcileDataRegistryNamespaceLabelsExistingNamespace(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: dataRegistryNamespaceName,
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, existing).Build()

	m := newCapabilitiesTestModule(t, true, true)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Name: dataRegistryNamespaceName}, ns)).To(Succeed())
	g.Expect(ns.Labels[dataRegistryEnabledLabelKey]).To(Equal(dataRegistryEnabledLabelValue))
}

func TestReconcileDataRegistryNamespaceSkippedWhenDisabled(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, true, false)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())

	nsList := &corev1.NamespaceList{}
	g.Expect(cl.List(context.Background(), nsList)).To(Succeed())
	g.Expect(nsList.Items).To(BeEmpty())
}

func TestReconcileDataRegistryNamespaceSkippedWhenDRRemovedInSpec(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityRemoved},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	// Even though env says DR=true, spec.capabilities says Removed — spec wins
	m := newCapabilitiesTestModule(t, true, true)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())

	nsList := &corev1.NamespaceList{}
	g.Expect(cl.List(context.Background(), nsList)).To(Succeed())
	g.Expect(nsList.Items).To(BeEmpty())
}

func TestReconcileDataRegistryNamespaceRemovesLabelWhenDisabled(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	feast.Spec.Capabilities = &componentApi.CapabilitiesSpec{
		FeatureStore: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityManaged},
		DataRegistry: componentApi.CapabilitySpec{ManagementState: componentApi.CapabilityRemoved},
	}
	existing := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: dataRegistryNamespaceName,
			Labels: map[string]string{
				dataRegistryEnabledLabelKey: dataRegistryEnabledLabelValue,
				"other-label":               "keep-me",
			},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast, existing).Build()

	m := newCapabilitiesTestModule(t, true, true)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())

	ns := &corev1.Namespace{}
	g.Expect(cl.Get(context.Background(), client.ObjectKey{Name: dataRegistryNamespaceName}, ns)).To(Succeed())
	g.Expect(ns.Labels).NotTo(HaveKey(dataRegistryEnabledLabelKey), "enabled label should be removed")
	g.Expect(ns.Labels["other-label"]).To(Equal("keep-me"), "other labels should be preserved")
}

func TestReconcileDataRegistryNamespaceNoopWhenDisabledAndNoNamespace(t *testing.T) {
	g := NewWithT(t)

	scheme := initNamespaceTestScheme()
	feast := newTestFeastOperator()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(feast).Build()

	m := newCapabilitiesTestModule(t, false, false)
	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Client:   cl,
	}

	g.Expect(m.reconcileDataRegistryNamespace(context.Background(), rr)).To(Succeed())
}
