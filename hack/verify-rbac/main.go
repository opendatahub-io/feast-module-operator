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

package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	"sigs.k8s.io/yaml"
)

type permission struct {
	APIGroup string
	Resource string
	Verb     string
}

func (p permission) String() string {
	group := p.APIGroup
	if group == "" {
		group = `""`
	}
	return fmt.Sprintf("groups=%s resources=%s verbs=%s", group, p.Resource, p.Verb)
}

func loadClusterRole(path string) (*rbacv1.ClusterRole, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	role := &rbacv1.ClusterRole{}
	if err := yaml.Unmarshal(data, role); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return role, nil
}

// loadClusterRoleByName parses a multi-document YAML file and returns the
// ClusterRole matching the given name. Returns an error if not found.
func loadClusterRoleByName(path, name string) (*rbacv1.ClusterRole, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	docs := strings.Split(string(data), "\n---\n")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		role := &rbacv1.ClusterRole{}
		if err := yaml.Unmarshal([]byte(doc), role); err != nil {
			continue
		}
		if role.Name == name {
			return role, nil
		}
	}

	return nil, fmt.Errorf("ClusterRole %q not found in %s", name, path)
}

// extractPermissions flattens a ClusterRole into a set of (apiGroup, resource, verb) tuples.
// Rules with resourceNames are expanded without the name constraint since the module operator
// grants broader (unscoped) access.
func extractPermissions(role *rbacv1.ClusterRole) map[permission]struct{} {
	perms := make(map[permission]struct{})
	for _, rule := range role.Rules {
		for _, group := range rule.APIGroups {
			for _, resource := range rule.Resources {
				for _, verb := range rule.Verbs {
					perms[permission{APIGroup: group, Resource: resource, Verb: verb}] = struct{}{}
				}
			}
		}
	}
	return perms
}

func main() {
	if len(os.Args) < 3 || len(os.Args) > 5 {
		fmt.Fprintf(os.Stderr, "Usage: %s <module-operator-role.yaml> <operand-role.yaml> [<chart-clusterrole.yaml> <role-name>]\n", os.Args[0])
		os.Exit(2)
	}

	moduleRolePath := os.Args[1]
	operandRolePath := os.Args[2]

	moduleRole, err := loadClusterRole(moduleRolePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	operandRole, err := loadClusterRole(operandRolePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	modulePerms := extractPermissions(moduleRole)
	operandPerms := extractPermissions(operandRole)
	// The operand reconciles Data Registry namespace labels via Update, so
	// the bundled role must keep namespace create/update permissions in
	// sync with the pinned Feast source RBAC markers. Catch a manifest
	// refresh that accidentally drops either permission.
	var missingNamespacePerms []permission
	for _, verb := range []string{"create", "update"} {
		p := permission{APIGroup: "", Resource: "namespaces", Verb: verb}
		if _, ok := operandPerms[p]; !ok {
			missingNamespacePerms = append(missingNamespacePerms, p)
		}
	}
	if len(missingNamespacePerms) > 0 {
		printMissing("bundled operand RBAC", "Data Registry namespace reconciliation", missingNamespacePerms)
		os.Exit(1)
	}

	var missing []permission
	for p := range operandPerms {
		if _, ok := modulePerms[p]; !ok {
			missing = append(missing, p)
		}
	}

	if len(missing) > 0 {
		printMissing("module operator RBAC", "the operand", missing)
		fmt.Fprintf(os.Stderr, "\nAdd the missing permissions to the kubebuilder:rbac annotations in:\n")
		fmt.Fprintf(os.Stderr, "  internal/controller/feastoperator/feastoperator_controller.go\n")
		fmt.Fprintf(os.Stderr, "Then run: make manifests\n")
		os.Exit(1)
	}

	fmt.Println("OK: module operator RBAC covers all operand permissions.")

	if len(os.Args) == 5 {
		chartPath := os.Args[3]
		roleName := os.Args[4]

		chartRole, err := loadClusterRoleByName(chartPath, roleName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		chartPerms := extractPermissions(chartRole)

		var chartMissing []permission
		for p := range modulePerms {
			if _, ok := chartPerms[p]; !ok {
				chartMissing = append(chartMissing, p)
			}
		}

		if len(chartMissing) > 0 {
			printMissing(fmt.Sprintf("chart ClusterRole %q", roleName), "the source role", chartMissing)
			fmt.Fprintf(os.Stderr, "\nRegenerate the chart with: make helm\n")
			os.Exit(1)
		}

		fmt.Printf("OK: chart ClusterRole %q is in sync with the source role.\n", roleName)
	}
}

func printMissing(target, source string, missing []permission) {
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].APIGroup != missing[j].APIGroup {
			return missing[i].APIGroup < missing[j].APIGroup
		}
		if missing[i].Resource != missing[j].Resource {
			return missing[i].Resource < missing[j].Resource
		}
		return missing[i].Verb < missing[j].Verb
	})

	fmt.Fprintf(os.Stderr, "FAIL: %s is missing %d permission(s) required by %s:\n\n", target, len(missing), source)

	grouped := map[string]map[string][]string{}
	for _, p := range missing {
		key := p.APIGroup + "/" + p.Resource
		if grouped[key] == nil {
			grouped[key] = map[string][]string{}
		}
		grouped[key]["verbs"] = append(grouped[key]["verbs"], p.Verb)
	}

	for _, p := range missing {
		key := p.APIGroup + "/" + p.Resource
		if verbs, ok := grouped[key]; ok {
			group := p.APIGroup
			if group == "" {
				group = `""`
			}
			fmt.Fprintf(os.Stderr, "  groups=%-30s resources=%-30s verbs=%s\n",
				group, p.Resource, strings.Join(verbs["verbs"], ";"))
			delete(grouped, key)
		}
	}
}
