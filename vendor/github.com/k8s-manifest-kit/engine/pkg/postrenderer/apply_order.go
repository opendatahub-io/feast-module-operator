package postrenderer

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/k8s-manifest-kit/engine/pkg/types"
)

//nolint:gochecknoglobals
var (
	orderFirst = []string{
		"Namespace",
		"ResourceQuota",
		"StorageClass",
		"CustomResourceDefinition",
		"ServiceAccount",
		"PodSecurityPolicy",
		"Role",
		"ClusterRole",
		"RoleBinding",
		"ClusterRoleBinding",
		"ConfigMap",
		"Secret",
		"Endpoints",
		"Service",
		"LimitRange",
		"PriorityClass",
		"PersistentVolume",
		"PersistentVolumeClaim",
		"Deployment",
		"StatefulSet",
		"CronJob",
		"PodDisruptionBudget",
	}

	orderLast = []string{
		"MutatingWebhookConfiguration",
		"ValidatingWebhookConfiguration",
	}

	kindOrder map[string]int
)

//nolint:gochecknoinits
func init() {
	kindOrder = make(map[string]int, len(orderFirst)+len(orderLast))

	for i, kind := range orderFirst {
		kindOrder[kind] = i - len(orderFirst)
	}

	for i, kind := range orderLast {
		kindOrder[kind] = i + 1
	}
}

// ApplyOrder returns a PostRenderer that sorts resources into dependency
// order for cluster application. Cluster-wide foundational resources
// (Namespace, CRD, ServiceAccount, etc.) come first; resources with many
// dependencies (webhooks) come last. Resources not in either list are
// placed in the middle, sorted by GVK string for stability.
func ApplyOrder() types.PostRenderer {
	return func(_ context.Context, objects []unstructured.Unstructured) ([]unstructured.Unstructured, error) {
		sort.SliceStable(objects, func(i, j int) bool {
			return compareOrder(objects[i], objects[j])
		})

		return objects, nil
	}
}

func compareOrder(a unstructured.Unstructured, b unstructured.Unstructured) bool {
	orderA := kindOrder[a.GetKind()]
	orderB := kindOrder[b.GetKind()]

	if orderA != orderB {
		return orderA < orderB
	}

	gvkA := gvkString(a)
	gvkB := gvkString(b)

	if gvkA != gvkB {
		return gvkA < gvkB
	}

	nsA := a.GetNamespace()
	nsB := b.GetNamespace()

	if nsA != nsB {
		return nsA < nsB
	}

	return a.GetName() < b.GetName()
}

func gvkString(obj unstructured.Unstructured) string {
	gvk := obj.GroupVersionKind()

	return fmt.Sprintf("%s/%s/%s", gvk.Group, gvk.Version, gvk.Kind)
}
