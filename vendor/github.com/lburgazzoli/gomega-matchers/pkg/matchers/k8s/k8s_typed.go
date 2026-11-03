package k8s

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Matcher wraps a Kubernetes client.Client and provides typed helper functions
// that work with typed Kubernetes objects (e.g., *corev1.ConfigMap) and return
// unstructured results compatible with Gomega assertions and JQ matchers.
type Matcher struct {
	client client.Client
	scheme *runtime.Scheme
}

// New creates a new typed Matcher wrapping the provided client.Client.
// The scheme is used to extract GVK information from typed objects.
func New(cli client.Client, scheme *runtime.Scheme) *Matcher {
	return &Matcher{
		client: cli,
		scheme: scheme,
	}
}

// Get retrieves a Kubernetes resource using a typed object.
// The GVK is automatically extracted from the object type using the scheme.
// Returns a function that can be used with Gomega's Eventually for async assertions.
//
// Example:
//
//	k := k8s.New(client, scheme)
//	Eventually(k.Get(&corev1.ConfigMap{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      "my-config",
//			Namespace: "default",
//		},
//	})).WithContext(ctx).Should(jq.Match(`.data.key == "value"`))
func (m *Matcher) Get(
	obj client.Object,
	opts ...client.GetOption,
) func(context.Context) (*unstructured.Unstructured, error) {
	return func(ctx context.Context) (*unstructured.Unstructured, error) {
		gvk, key, err := m.extractGVKAndKey(obj)
		if err != nil {
			return nil, err
		}

		result := &unstructured.Unstructured{}
		result.SetGroupVersionKind(gvk)

		err = m.client.Get(ctx, key.ToNamespacedName(), result, opts...)
		if err != nil {
			return nil, err
		}

		return result, nil
	}
}

// List retrieves a list of Kubernetes resources using a typed list object.
// The GVK is automatically extracted from the list type using the scheme.
// Returns a function that can be used with Gomega's Eventually for async assertions.
//
// Example:
//
//	k := k8s.New(client, scheme)
//	Eventually(k.List(&corev1.ConfigMapList{},
//		client.InNamespace("default"),
//		client.MatchingLabels{"app": "myapp"},
//	)).WithContext(ctx).Should(jq.Match(`.items | length > 0`))
func (m *Matcher) List(
	list client.ObjectList,
	opts ...client.ListOption,
) func(context.Context) (*unstructured.UnstructuredList, error) {
	return func(ctx context.Context) (*unstructured.UnstructuredList, error) {
		gvks, _, err := m.scheme.ObjectKinds(list)
		if err != nil {
			return nil, fmt.Errorf("failed to get GVK from list: %w", err)
		}

		if len(gvks) == 0 {
			return nil, fmt.Errorf("no GVK found for list type %T", list)
		}

		result := &unstructured.UnstructuredList{}
		result.SetGroupVersionKind(gvks[0])

		err = m.client.List(ctx, result, opts...)
		if err != nil {
			return nil, err
		}

		return result, nil
	}
}

// Delete deletes a Kubernetes resource using a typed object.
// The GVK and object key are automatically extracted from the object.
// Returns a function that can be used with Gomega's Eventually for async assertions.
//
// Example:
//
//	k := k8s.New(client, scheme)
//	Eventually(k.Delete(&corev1.ConfigMap{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      "my-config",
//			Namespace: "default",
//		},
//	})).WithContext(ctx).Should(Succeed())
func (m *Matcher) Delete(
	obj client.Object,
	opts ...client.DeleteOption,
) func(context.Context) error {
	return func(ctx context.Context) error {
		gvk, _, err := m.extractGVKAndKey(obj)
		if err != nil {
			return err
		}

		target := &unstructured.Unstructured{}
		target.SetGroupVersionKind(gvk)
		target.SetName(obj.GetName())
		target.SetNamespace(obj.GetNamespace())

		return m.client.Delete(ctx, target, opts...)
	}
}

// Update retrieves a Kubernetes resource, applies an update function, and updates it.
// This follows the Komega-style pattern for type-safe updates.
// Returns a function that can be used with Gomega's Eventually for async assertions.
//
// Example:
//
//	k := k8s.New(client, scheme)
//	Eventually(k.Update(&corev1.ConfigMap{
//		ObjectMeta: metav1.ObjectMeta{
//			Name:      "my-config",
//			Namespace: "default",
//		},
//	}, func(obj client.Object) {
//		cm := obj.(*corev1.ConfigMap)
//		cm.Data["key"] = "new-value"
//	})).WithContext(ctx).Should(jq.Match(`.data.key == "new-value"`))
func (m *Matcher) Update(
	obj client.Object,
	updateFunc func(client.Object),
	opts ...client.UpdateOption,
) func(context.Context) (*unstructured.Unstructured, error) {
	return func(ctx context.Context) (*unstructured.Unstructured, error) {
		return m.doUpdate(ctx, obj, updateFunc, opts...)
	}
}

func (m *Matcher) doUpdate(
	ctx context.Context,
	obj client.Object,
	updateFunc func(client.Object),
	opts ...client.UpdateOption,
) (*unstructured.Unstructured, error) {
	gvk, key, err := m.extractGVKAndKey(obj)
	if err != nil {
		return nil, err
	}

	current, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return nil, errors.New("failed to convert deep copy to client.Object")
	}

	err = m.client.Get(ctx, key.ToNamespacedName(), current)
	if err != nil {
		return nil, fmt.Errorf("failed to get resource for update: %w", err)
	}

	updateFunc(current)

	err = m.client.Update(ctx, current, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to update resource: %w", err)
	}

	result := &unstructured.Unstructured{}
	result.SetGroupVersionKind(gvk)

	err = m.client.Get(ctx, key.ToNamespacedName(), result)
	if err != nil {
		return nil, fmt.Errorf("failed to get updated resource: %w", err)
	}

	return result, nil
}

// extractGVKAndKey extracts GroupVersionKind and ObjectKey from a typed Kubernetes object.
func (m *Matcher) extractGVKAndKey(obj client.Object) (schema.GroupVersionKind, ObjectKey, error) {
	gvks, _, err := m.scheme.ObjectKinds(obj)
	if err != nil {
		return schema.GroupVersionKind{}, ObjectKey{}, fmt.Errorf("failed to get GVK from object: %w", err)
	}

	if len(gvks) == 0 {
		return schema.GroupVersionKind{}, ObjectKey{}, fmt.Errorf("no GVK found for object type %T", obj)
	}

	key := ObjectKey{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}

	return gvks[0], key, nil
}
