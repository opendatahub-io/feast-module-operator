package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
)

func TestDCHPermissionsInSourceAndChart(t *testing.T) {
	root := filepath.Join("..", "..")
	module, err := loadClusterRole(filepath.Join(root, "config", "rbac", "role.yaml"))
	require.NoError(t, err)
	chart, err := loadClusterRoleByName(filepath.Join(root, "config", "chart", "templates",
		"rbac.authorization.k8s.io_v1_clusterrole.yaml"), "opendatahub-feast-manager-role")
	require.NoError(t, err)
	controller, err := loadClusterRole(filepath.Join(root, "config", "manifests", "dataconnecthub", "rbac", "role.yaml"))
	require.NoError(t, err)
	for name, role := range map[string]*rbacv1.ClusterRole{"source": module, "chart": chart, "controller bundle": controller} {
		t.Run(name, func(t *testing.T) {
			granted := extractPermissions(role)
			for _, verb := range []string{"create", "delete"} {
				require.True(t, permissionCovered(granted, permission{
					APIGroup: "dataconnecthub.opendatahub.io", Resource: "initdataconnectiontypes", Verb: verb,
				}), "DCH initialization and uninstall permissions must be present in source, chart and controller bundle: %s", verb)
			}
		})
	}
	// Keep the existing module's named-bind regression separate from the
	// controller's requirements; upstream DCH removed its explicit bind grants.
	for _, role := range []*rbacv1.ClusterRole{module, chart} {
		granted := extractPermissions(role)
		for _, name := range []string{"dch-flight-service-secret-access", "dch-read", "dch-read-write", "dch-rest-service-secret-access"} {
			require.True(t, permissionCovered(granted, permission{
				APIGroup: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "bind", ResourceName: name,
			}), name)
		}
		for p := range granted {
			if p.Resource == "clusterroles" && p.Verb == "bind" {
				require.NotEmpty(t, p.ResourceName, "bind grants must remain limited to named roles")
			}
		}
	}
}

func TestPermissionCoveredResourceNames(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		grantedNames, requiredNames []string
		want                        bool
	}{
		{name: "unrelated role bind is not sufficient", grantedNames: []string{"system:auth-delegator"}, requiredNames: []string{"dch-read"}},
		{name: "all named roles must be covered", grantedNames: []string{"dch-read"}, requiredNames: []string{"dch-read", "dch-read-write"}},
		{name: "matching named roles", grantedNames: []string{"dch-read", "dch-read-write"}, requiredNames: []string{"dch-read"}, want: true},
		{name: "unscoped grant covers named roles", requiredNames: []string{"dch-read", "dch-read-write"}, want: true},
		{name: "named grant does not cover unscoped access", grantedNames: []string{"dch-read"}},
		{name: "resource name star is literal", grantedNames: []string{"*"}, requiredNames: []string{"dch-read"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			role := func(names []string) *rbacv1.ClusterRole {
				return &rbacv1.ClusterRole{Rules: []rbacv1.PolicyRule{{
					APIGroups: []string{"rbac.authorization.k8s.io"}, Resources: []string{"clusterroles"},
					Verbs: []string{"bind"}, ResourceNames: names,
				}}}
			}
			granted := extractPermissions(role(tc.grantedNames))
			covered := true
			for required := range extractPermissions(role(tc.requiredNames)) {
				covered = covered && permissionCovered(granted, required)
			}
			require.Equal(t, tc.want, covered)
		})
	}
}

// DCH #505 added warning Events for tenant-onboarding conflicts. This is
// required when refreshing the DCH dependency even without namespace changes.
func TestModuleCanDelegateDCHTenantWarningEvents(t *testing.T) {
	root := filepath.Join("..", "..")
	module, err := loadClusterRole(filepath.Join(root, "config", "rbac", "role.yaml"))
	require.NoError(t, err)
	chart, err := loadClusterRoleByName(filepath.Join(root, "config", "chart", "templates",
		"rbac.authorization.k8s.io_v1_clusterrole.yaml"), "opendatahub-feast-manager-role")
	require.NoError(t, err)
	for _, role := range []*rbacv1.ClusterRole{module, chart} {
		granted := extractPermissions(role)
		for _, verb := range []string{"create", "patch"} {
			require.True(t, permissionCovered(granted, permission{
				APIGroup: "events.k8s.io", Resource: "events", Verb: verb,
			}), "source and chart must be able to grant DCH tenant warning Events")
		}
		for _, verb := range []string{"get", "list", "watch", "update", "delete"} {
			require.False(t, permissionCovered(granted, permission{
				APIGroup: "events.k8s.io", Resource: "events", Verb: verb,
			}), "event permissions must remain limited to create and patch")
		}
	}
}
