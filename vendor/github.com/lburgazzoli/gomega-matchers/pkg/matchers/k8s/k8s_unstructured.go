package k8s

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// UnstructuredMatcher wraps a Kubernetes client.Client and provides GVK-based
// helper functions that return matcher-compatible functions for use with Gomega assertions.
type UnstructuredMatcher struct {
	client client.Client
}

// NewUnstructured creates a new UnstructuredMatcher wrapping the provided client.Client.
func NewUnstructured(cli client.Client) *UnstructuredMatcher {
	return &UnstructuredMatcher{
		client: cli,
	}
}

// Get returns a function that retrieves a Kubernetes resource by GVK and ObjectKey.
// The returned function is compatible with Gomega's Eventually() and Expect().
//
// For cluster-scoped resources, use Named("resource-name").
//
// For namespaced resources, use Named("resource-name").InNamespace("namespace")
// or NamespacedNamed("namespace", "name").
//
// Example:
//
//	podGVK := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
//	Eventually(k.Get(podGVK, Named("my-pod").InNamespace("default"))).
//		WithContext(ctx).
//		Should(jq.Match(".status.phase == \"Running\""))
func (m *UnstructuredMatcher) Get(
	gvk schema.GroupVersionKind,
	key ObjectKey,
	opts ...client.GetOption,
) func(context.Context) (*unstructured.Unstructured, error) {
	return func(ctx context.Context) (*unstructured.Unstructured, error) {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)

		err := m.client.Get(ctx, key.ToNamespacedName(), obj, opts...)
		if err != nil {
			return nil, err
		}

		return obj, nil
	}
}

// List returns a function that retrieves a list of Kubernetes resources by GVK.
// The returned function is compatible with Gomega's Eventually() and Expect().
//
// Options can include client.InNamespace() to filter by namespace,
// client.MatchingLabels() for label selectors, etc.
//
// Example:
//
//	podGVK := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
//	Eventually(k.List(podGVK, client.InNamespace("default"))).
//		WithContext(ctx).
//		Should(jq.Match(".items | length > 0"))
func (m *UnstructuredMatcher) List(
	gvk schema.GroupVersionKind,
	opts ...client.ListOption,
) func(context.Context) (*unstructured.UnstructuredList, error) {
	return func(ctx context.Context) (*unstructured.UnstructuredList, error) {
		list := &unstructured.UnstructuredList{}
		list.SetGroupVersionKind(gvk)

		err := m.client.List(ctx, list, opts...)
		if err != nil {
			return nil, err
		}

		return list, nil
	}
}

// Delete returns a function that deletes a Kubernetes resource by GVK and ObjectKey.
// The returned function is compatible with Gomega's Eventually() and Expect().
//
// For cluster-scoped resources, use Named("resource-name").
//
// For namespaced resources, use Named("resource-name").InNamespace("namespace")
// or NamespacedNamed("namespace", "name").
//
// Options can include client.GracePeriodSeconds() for deletion grace period, etc.
//
// Example:
//
//	podGVK := schema.GroupVersionKind{Version: "v1", Kind: "Pod"}
//	Expect(k.Delete(podGVK, Named("my-pod").InNamespace("default"))(ctx)).To(Succeed())
func (m *UnstructuredMatcher) Delete(
	gvk schema.GroupVersionKind,
	key ObjectKey,
	opts ...client.DeleteOption,
) func(context.Context) error {
	return func(ctx context.Context) error {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		obj.SetName(key.Name)
		obj.SetNamespace(key.Namespace)

		return m.client.Delete(ctx, obj, opts...)
	}
}
