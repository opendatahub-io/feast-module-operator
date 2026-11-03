package cluster

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/odh-platform-utilities/api/common"
	"github.com/opendatahub-io/odh-platform-utilities/pkg/controller/conditions"
)

const (
	managedAddonCatalogName = "addon-managed-odh-catalog"
	rhoaiOperatorPackage    = "rhods-operator"
)

const olmV1Group = "olm.operatorframework.io"

const (
	clusterExtensionInstalledConditionType = "Installed"
	clusterExtensionInstalledReason        = "Succeeded"
)

// OLMv1 GVKs use unstructured clients so this package does not import
// github.com/operator-framework/operator-controller/api.
//
//nolint:gochecknoglobals // Immutable GVK constants.
var (
	platformClusterCatalogGVK = schema.GroupVersionKind{
		Group: olmV1Group, Version: "v1", Kind: "ClusterCatalog",
	}
	platformClusterExtensionGVK = schema.GroupVersionKind{
		Group: olmV1Group, Version: "v1", Kind: "ClusterExtension",
	}
)

// clusterCatalogExists reports whether a cluster-scoped ClusterCatalog exists.
// IsNoMatchError and NotFound are treated as absent (false, nil).
func clusterCatalogExists(ctx context.Context, cli client.Reader, name string) (bool, error) {
	catalog := &unstructured.Unstructured{}
	catalog.SetGroupVersionKind(platformClusterCatalogGVK)

	err := cli.Get(ctx, client.ObjectKey{Name: name}, catalog)
	if err == nil {
		return true, nil
	}

	if meta.IsNoMatchError(err) {
		return false, nil
	}

	if client.IgnoreNotFound(err) != nil {
		return false, err
	}

	return false, nil
}

// ClusterExtensionInstallsPackage reports whether a ClusterExtension requests
// the given OLM package. Matching uses spec.source.catalog.packageName when
// spec.source.sourceType is "Catalog"; the ClusterExtension resource name is
// arbitrary. When installNamespace is non-empty, the extension must also
// target that namespace (spec.namespace).
//
// List errors are returned as-is, including [meta.IsNoMatchError] when the
// ClusterExtension CRD is absent. Callers decide how to handle API absence.
func ClusterExtensionInstallsPackage(
	ctx context.Context, cli client.Reader, packageName, installNamespace string,
) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(platformClusterExtensionGVK)

	err := cli.List(ctx, list)
	if err != nil {
		return false, err
	}

	for i := range list.Items {
		match, matchErr := clusterExtensionInstallsPackage(&list.Items[i], packageName, installNamespace)
		if matchErr != nil {
			return false, matchErr
		}

		if match {
			return true, nil
		}
	}

	return false, nil
}

func clusterExtensionInstallsPackage(
	ext *unstructured.Unstructured, packageName, installNamespace string,
) (bool, error) {
	if installNamespace != "" {
		ns, found, err := unstructured.NestedString(ext.Object, "spec", "namespace")
		if err != nil {
			return false, fmt.Errorf("read ClusterExtension spec.namespace: %w", err)
		}

		if !found || ns != installNamespace {
			return false, nil
		}
	}

	sourceType, found, err := unstructured.NestedString(ext.Object, "spec", "source", "sourceType")
	if err != nil {
		return false, fmt.Errorf("read ClusterExtension spec.source.sourceType: %w", err)
	}

	if !found || sourceType != "Catalog" {
		return false, nil
	}

	pkg, found, err := unstructured.NestedString(ext.Object, "spec", "source", "catalog", "packageName")
	if err != nil {
		return false, fmt.Errorf("read ClusterExtension spec.source.catalog.packageName: %w", err)
	}

	return found && pkg == packageName, nil
}

// OperatorInstalledViaClusterExtension reports whether a ClusterExtension has
// successfully installed the given OLM package. Matching uses
// spec.source.catalog.packageName when spec.source.sourceType is "Catalog".
// Installation is confirmed via status.conditions Installed=True with reason
// Succeeded. Version is read from status.install.bundle.version when present.
//
// List errors are returned as-is, including [meta.IsNoMatchError] when the
// ClusterExtension CRD is absent. When no installed extension matches, it
// returns (nil, nil). Prefer olm.OperatorExists for dependency checks.
func OperatorInstalledViaClusterExtension(
	ctx context.Context, cli client.Reader, packageName string,
) (*OperatorInfo, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(platformClusterExtensionGVK)

	err := cli.List(ctx, list)
	if err != nil {
		return nil, err
	}

	for i := range list.Items {
		ext := &list.Items[i]

		match, matchErr := clusterExtensionInstallsPackage(ext, packageName, "")
		if matchErr != nil {
			return nil, matchErr
		}

		if !match {
			continue
		}

		installed, installedErr := clusterExtensionInstalled(ext)
		if installedErr != nil {
			return nil, installedErr
		}

		if !installed {
			continue
		}

		version, versionErr := clusterExtensionInstalledVersion(ext)
		if versionErr != nil {
			return nil, versionErr
		}

		return NewOperatorInfo(version), nil
	}

	return nil, nil //nolint:nilnil // Not found; olm.OperatorExists maps to ErrOperatorNotInstalled.
}

func clusterExtensionInstalled(ext *unstructured.Unstructured) (bool, error) {
	statusRaw, found, err := unstructured.NestedMap(ext.Object, "status")
	if err != nil {
		return false, fmt.Errorf("read ClusterExtension status: %w", err)
	}

	if !found {
		return false, nil
	}

	var status common.Status

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(statusRaw, &status)
	if err != nil {
		return false, fmt.Errorf("convert ClusterExtension status: %w", err)
	}

	cond := conditions.FindStatusCondition(&status, clusterExtensionInstalledConditionType)

	return cond != nil &&
		cond.Status == metav1.ConditionTrue &&
		cond.Reason == clusterExtensionInstalledReason, nil
}

func clusterExtensionInstalledVersion(ext *unstructured.Unstructured) (string, error) {
	version, found, err := unstructured.NestedString(ext.Object, "status", "install", "bundle", "version")
	if err != nil {
		return "", fmt.Errorf("read ClusterExtension status.install.bundle.version: %w", err)
	}

	if !found {
		return "", nil
	}

	return version, nil
}
