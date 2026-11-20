package chartgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"sigs.k8s.io/yaml"
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
	if !strings.Contains(gates, "namespace: {{ default .Release.Namespace .Values.namespace }}") {
		t.Errorf("upgrade-gates.yaml is missing the chart namespace:\n%s", gates)
	}
}

// TestRunGeneratedUpgradeGatesRenderData renders the generated template with a
// non-empty gate map and checks that YAML-sensitive keys remain strings.
func TestRunGeneratedUpgradeGatesRenderData(t *testing.T) {
	out := generate(t, "")
	gates := readFile(t, filepath.Join(out, templatesDirName, upgradeGatesTplFilename))

	tmpl, err := template.New(upgradeGatesTplFilename).
		Option("missingkey=error").
		Funcs(sprig.TxtFuncMap()).
		Parse(gates)
	if err != nil {
		t.Fatalf("parsing upgrade-gates template: %v", err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, map[string]any{
		"Release": map[string]any{"Namespace": "release-ns"},
		"Values": map[string]any{
			"namespace": "custom-ns",
			"upgradeGates": map[string]string{
				"true":   "required",
				"normal": "enabled",
			},
		},
	}); err != nil {
		t.Fatalf("rendering upgrade-gates template: %v", err)
	}

	var configMap struct {
		Kind     string `json:"kind"`
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
	}
	if err := yaml.Unmarshal(rendered.Bytes(), &configMap); err != nil {
		t.Fatalf("decoding rendered upgrade gates: %v\n%s", err, rendered.String())
	}

	if configMap.Kind != "ConfigMap" {
		t.Errorf("kind = %q, want ConfigMap", configMap.Kind)
	}
	if configMap.Metadata.Name != "feastoperator-upgrade-gates" {
		t.Errorf("name = %q, want feastoperator-upgrade-gates", configMap.Metadata.Name)
	}
	if configMap.Metadata.Namespace != "custom-ns" {
		t.Errorf("namespace = %q, want custom-ns", configMap.Metadata.Namespace)
	}
	if configMap.Data["true"] != "required" {
		t.Errorf("gate \"true\" = %q, want required\n%s", configMap.Data["true"], rendered.String())
	}
	if configMap.Data["normal"] != "enabled" {
		t.Errorf("gate \"normal\" = %q, want enabled\n%s", configMap.Data["normal"], rendered.String())
	}
	if !strings.Contains(rendered.String(), `"true": "required"`) {
		t.Errorf("rendered YAML did not quote the YAML-sensitive key:\n%s", rendered.String())
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

// TestImageSpecForDigest keeps a digest reference whole, because splitting it
// would let an image.tag override render a tag after "@sha256".
func TestImageSpecForDigest(t *testing.T) {
	spec := imageSpecFor("registry.example/manager@sha256:" + strings.Repeat("a", 64))

	if spec.FullRef == "" {
		t.Fatalf("digest reference was not stored in fullRef: %+v", spec)
	}
	if spec.Repository != "registry.example/manager" {
		t.Errorf("repository = %q, want registry.example/manager", spec.Repository)
	}
	if spec.Tag != "" {
		t.Errorf("tag = %q, want empty", spec.Tag)
	}
}

// TestImageSpecForTag keeps the repository and tag split for tagged references.
func TestImageSpecForTag(t *testing.T) {
	spec := imageSpecFor("quay.io/opendatahub/odh-feast-module-operator:odh-stable")

	if spec.FullRef != "" {
		t.Errorf("fullRef = %q, want empty", spec.FullRef)
	}
	if spec.Repository != "quay.io/opendatahub/odh-feast-module-operator" {
		t.Errorf("repository = %q", spec.Repository)
	}
	if spec.Tag != "odh-stable" {
		t.Errorf("tag = %q, want odh-stable", spec.Tag)
	}
}
