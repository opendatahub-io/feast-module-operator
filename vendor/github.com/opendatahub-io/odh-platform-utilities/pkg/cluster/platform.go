package cluster

import (
	"context"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OLM GVKs are intentionally duplicated from cluster/olm to avoid an
// import cycle (olm imports cluster for OperatorInfo).
//
//nolint:gochecknoglobals // Immutable GVK constants.
var (
	platformOperatorConditionGVK = schema.GroupVersionKind{
		Group: "operators.coreos.com", Version: "v2", Kind: "OperatorCondition",
	}
	platformCatalogSourceGVK = schema.GroupVersionKind{
		Group: "operators.coreos.com", Version: "v1alpha1", Kind: "CatalogSource",
	}
)

// DetectPlatform determines the product distribution (platform variant)
// that is deploying the controller.
//
// Detection follows this precedence:
//  1. If platformType is a recognized non-empty string ("OpenDataHub",
//     "ManagedRHOAI", "SelfManagedRHOAI", "XKS"), the corresponding
//     [Platform] constant is returned immediately.
//  2. Otherwise, OLM resources are probed to auto-detect:
//     a. If an "addon-managed-odh-catalog" CatalogSource exists in
//     operatorNamespace, or a cluster-scoped ClusterCatalog with that name
//     exists (OLMv1) → [ManagedRhoai].
//     b. If a "rhods-operator" OperatorCondition exists, or a ClusterExtension
//     with spec.source.catalog.packageName "rhods-operator" exists (OLMv1)
//     → [SelfManagedRhoai].
//     c. Fallback → [OpenDataHub].
//
// The platformType parameter typically comes from the ODH_PLATFORM_TYPE
// environment variable (set in the operator CSV). The operatorNamespace
// parameter is used for the ManagedRhoai CatalogSource lookup and may be
// empty (defaults to "redhat-ods-operator").
//
// This function is a transitional necessity. In the fully-realized module
// architecture, modules should prefer reading platform info from their
// projected CR config (set by the orchestrator) rather than calling
// detection directly.
func DetectPlatform(ctx context.Context, cli client.Reader, platformType, operatorNamespace string) (Platform, error) {
	switch platformType {
	case "OpenDataHub":
		return OpenDataHub, nil
	case "ManagedRHOAI":
		return ManagedRhoai, nil
	case "SelfManagedRHOAI":
		return SelfManagedRhoai, nil
	case string(XKS):
		return XKS, nil
	}

	if operatorNamespace == "" {
		operatorNamespace = "redhat-ods-operator"
	}

	managed, err := detectManagedRhoai(ctx, cli, operatorNamespace)
	if err != nil {
		return "", err
	}

	if managed {
		return ManagedRhoai, nil
	}

	return detectSelfManaged(ctx, cli)
}

func detectManagedRhoai(ctx context.Context, cli client.Reader, operatorNamespace string) (bool, error) {
	cs := &unstructured.Unstructured{}
	cs.SetGroupVersionKind(platformCatalogSourceGVK)

	err := cli.Get(ctx, client.ObjectKey{
		Name:      managedAddonCatalogName,
		Namespace: operatorNamespace,
	}, cs)
	if err == nil {
		return true, nil
	}

	if meta.IsNoMatchError(err) {
		return clusterCatalogExists(ctx, cli, managedAddonCatalogName)
	}

	if client.IgnoreNotFound(err) != nil {
		return false, err
	}

	return clusterCatalogExists(ctx, cli, managedAddonCatalogName)
}

func detectSelfManaged(ctx context.Context, cli client.Reader) (Platform, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(platformOperatorConditionGVK)

	err := cli.List(ctx, list)
	switch {
	case err == nil:
		for _, item := range list.Items {
			if strings.HasPrefix(item.GetName(), rhoaiOperatorPackage+".") {
				return SelfManagedRhoai, nil
			}
		}
	case meta.IsNoMatchError(err):
		// OLMv0 CRD absent; try OLMv1 ClusterExtension below.
	default:
		ignErr := client.IgnoreNotFound(err)
		if ignErr != nil {
			return "", ignErr
		}
	}

	found, err := ClusterExtensionInstallsPackage(ctx, cli, rhoaiOperatorPackage, "")
	if meta.IsNoMatchError(err) {
		return OpenDataHub, nil
	}

	if err != nil {
		return "", err
	}

	if found {
		return SelfManagedRhoai, nil
	}

	return OpenDataHub, nil
}
