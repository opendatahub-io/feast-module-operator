package chartgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testInput = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: opendatahub-feast-operator
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: manager
        image: controller:latest
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-egress
  namespace: opendatahub-feast-system
spec:
  podSelector: {}
  policyTypes:
  - Egress
`

func generate(t *testing.T, image string) string {
	t.Helper()

	out := t.TempDir()
	if err := run(strings.NewReader(testInput), out, defaultChartName, defaultChartVer, image); err != nil {
		t.Fatalf("run: %v", err)
	}

	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(content)
}

// TestRunGeneratesUpgradeGates guards the upgrade-gate template: it is not part
// of the kustomize output, so it is lost whenever `make helm` recreates the
// chart tree unless chartgen writes it.
func TestRunGeneratesUpgradeGates(t *testing.T) {
	out := generate(t, "")

	gates := readFile(t, filepath.Join(out, templatesDirName, upgradeGatesTplFilename))
	if !strings.Contains(gates, "platform.opendatahub.io/upgrade-gate") {
		t.Errorf("upgrade-gates.yaml is missing the upgrade-gate label:\n%s", gates)
	}
	if !strings.Contains(gates, "feastoperator-upgrade-gates") {
		t.Errorf("upgrade-gates.yaml is missing the ConfigMap name:\n%s", gates)
	}
}

// TestRunImageFlagOverridesDeployment verifies that the published image used by
// the platform operator wins over the development image in config/manager.
func TestRunImageFlagOverridesDeployment(t *testing.T) {
	out := generate(t, "quay.io/opendatahub/odh-feast-module-operator:odh-stable")

	values := readFile(t, filepath.Join(out, valuesYAMLFilename))
	if !strings.Contains(values, "repository: quay.io/opendatahub/odh-feast-module-operator") {
		t.Errorf("values.yaml does not use the image override:\n%s", values)
	}
	if !strings.Contains(values, "tag: odh-stable") {
		t.Errorf("values.yaml does not use the image tag override:\n%s", values)
	}
}

// TestRunWithoutImageFlagKeepsDeploymentImage documents the default behaviour
// when no override is supplied.
func TestRunWithoutImageFlagKeepsDeploymentImage(t *testing.T) {
	out := generate(t, "")

	values := readFile(t, filepath.Join(out, valuesYAMLFilename))
	if !strings.Contains(values, "repository: controller") {
		t.Errorf("values.yaml does not keep the Deployment image:\n%s", values)
	}
	if !strings.Contains(values, "tag: latest") {
		t.Errorf("values.yaml does not keep the Deployment image tag:\n%s", values)
	}
}

// TestRunTemplatesNetworkPolicyNamespace guards the namespace handling for
// resources that are grouped into a single template file.
func TestRunTemplatesNetworkPolicyNamespace(t *testing.T) {
	out := generate(t, "")

	policy := readFile(t, filepath.Join(out, templatesDirName, "networking.k8s.io_v1_networkpolicy.yaml"))
	if !strings.Contains(policy, "{{ default .Release.Namespace .Values.namespace }}") {
		t.Errorf("NetworkPolicy namespace is not templated:\n%s", policy)
	}
}
