package chartgen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	gvk "github.com/opendatahub-io/odh-platform-utilities/framework/cluster/gvk"
)

// TestRunTemplatesImagePullPolicy guards that the generated Deployment
// references the image.pullPolicy Helm value instead of hardcoding Always, so
// local-image workflows can render a no-forced-pull policy.
func TestRunTemplatesImagePullPolicy(t *testing.T) {
	out := generate(t, "")

	dep := readFile(t, filepath.Join(out, templatesDirName, "apps_v1_deployment.yaml"))
	if !strings.Contains(dep, "imagePullPolicy: {{ .Values.image.pullPolicy }}") {
		t.Errorf("deployment template does not reference image.pullPolicy:\n%s", dep)
	}
	if strings.Contains(dep, "imagePullPolicy: Always") {
		t.Errorf("deployment template hardcodes the pull policy:\n%s", dep)
	}
}

// TestGeneratedDeploymentRendersPullPolicy renders the generated Deployment
// template with the default values and with local-image overrides and decodes
// the resulting container fields.
func TestGeneratedDeploymentRendersPullPolicy(t *testing.T) {
	out := generate(t, "")

	dep := readFile(t, filepath.Join(out, templatesDirName, "apps_v1_deployment.yaml"))

	render := func(t *testing.T, mutate func(map[string]any)) appsv1.Deployment {
		t.Helper()

		values := DefaultValues()
		values.Namespace = "test-ns"
		raw, err := yaml.Marshal(values)
		if err != nil {
			t.Fatalf("marshaling values: %v", err)
		}

		var valuesMap map[string]any
		if err := yaml.Unmarshal(raw, &valuesMap); err != nil {
			t.Fatalf("unmarshaling values: %v", err)
		}

		if mutate != nil {
			mutate(valuesMap)
		}

		data := map[string]any{
			"Release": map[string]any{"Namespace": "test-ns", "Name": "rel", "Service": "helm"},
			"Chart":   map[string]any{"Name": defaultChartName, "Version": defaultChartVer},
			"Values":  valuesMap,
		}

		root := template.New("root")
		funcs := sprig.TxtFuncMap()
		funcs["include"] = func(name string, v any) (string, error) {
			var b bytes.Buffer

			if err := root.ExecuteTemplate(&b, name, v); err != nil {
				return "", err
			}

			return b.String(), nil
		}
		funcs["toYaml"] = func(v any) (string, error) {
			b, err := yaml.Marshal(v)

			return string(b), err
		}
		root = root.Funcs(funcs)

		// The helpers only contain named defines, so appending the deployment
		// body makes it the root template while keeps the defines reachable
		// through include.
		if _, err := root.Parse(helpersTpl + "\n" + dep); err != nil {
			t.Fatalf("parsing deployment template: %v", err)
		}

		var rendered bytes.Buffer
		if err := root.Execute(&rendered, data); err != nil {
			t.Fatalf("rendering deployment template: %v", err)
		}

		var deployment appsv1.Deployment
		if err := yaml.Unmarshal(rendered.Bytes(), &deployment); err != nil {
			t.Fatalf("decoding rendered deployment: %v\n%s", err, rendered.String())
		}

		return deployment
	}

	t.Run("default", func(t *testing.T) {
		deployment := render(t, nil)
		containers := deployment.Spec.Template.Spec.Containers
		if len(containers) != 1 {
			t.Fatalf("containers = %d, want 1", len(containers))
		}

		if got := string(containers[0].ImagePullPolicy); got != "Always" {
			t.Errorf("imagePullPolicy = %q, want Always", got)
		}

		if got := containers[0].Image; got != "controller:latest" {
			t.Errorf("image = %q, want controller:latest", got)
		}
	})

	t.Run("local image", func(t *testing.T) {
		deployment := render(t, func(values map[string]any) {
			image := values["image"].(map[string]any)
			image["repository"] = "quay.io/opendatahub/odh-feast-module-operator"
			image["tag"] = "kind-local-123"
			image["pullPolicy"] = "Never"
		})
		containers := deployment.Spec.Template.Spec.Containers
		if len(containers) != 1 {
			t.Fatalf("containers = %d, want 1", len(containers))
		}

		if got := string(containers[0].ImagePullPolicy); got != "Never" {
			t.Errorf("imagePullPolicy = %q, want Never", got)
		}

		if got := containers[0].Image; got != "quay.io/opendatahub/odh-feast-module-operator:kind-local-123" {
			t.Errorf("image = %q", got)
		}
	})
}

// TestRealModuleDeploymentSourceHasNoManifestCopying checks the real module
// Deployment source rather than a minimal fixture that never contained an init
// container: copying is removed and the manager reads /manifests directly
// with a read-only root filesystem.
func TestRealModuleDeploymentSourceHasNoManifestCopying(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "manager", "manager.yaml"))
	if err != nil {
		t.Fatalf("reading config/manager/manager.yaml: %v", err)
	}

	resources, err := decodeResources(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decoding manager.yaml: %v", err)
	}

	var deployment *unstructured.Unstructured

	for i := range resources {
		if resources[i].GroupVersionKind() == gvk.Deployment {
			deployment = &resources[i]

			break
		}
	}

	if deployment == nil {
		t.Fatal("no Deployment found in config/manager/manager.yaml")
	}

	if initContainers, found, _ := unstructured.NestedSlice(
		deployment.Object, "spec", "template", "spec", "initContainers",
	); found && len(initContainers) > 0 {
		t.Errorf("Deployment still has init containers: %v", initContainers)
	}

	volumes, found, _ := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "volumes")
	if !found {
		t.Fatal("Deployment has no volumes")
	}

	for _, v := range volumes {
		volume, ok := v.(map[string]any)
		if !ok {
			continue
		}

		if name, _ := volume["name"].(string); name == "manifests" {
			t.Errorf("Deployment still declares the manifests volume: %v", volume)
		}
	}

	containers, found, _ := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	if !found || len(containers) == 0 {
		t.Fatal("Deployment has no containers")
	}

	manager, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatal("manager container is not a map")
	}

	if mounts, ok := manager["volumeMounts"].([]any); ok {
		for _, m := range mounts {
			mount, ok := m.(map[string]any)
			if !ok {
				continue
			}

			if name, _ := mount["name"].(string); name == "manifests" {
				t.Errorf("manager container still mounts the manifests volume: %v", mount)
			}
		}
	}

	configMountedReadOnly := false

	if mounts, ok := manager["volumeMounts"].([]any); ok {
		for _, m := range mounts {
			mount, ok := m.(map[string]any)
			if !ok {
				continue
			}

			if name, _ := mount["name"].(string); name == "config" {
				configMountedReadOnly, _ = mount["readOnly"].(bool)
			}
		}
	}

	if !configMountedReadOnly {
		t.Error("manager container does not mount the config volume read-only")
	}

	readOnlyRoot := false
	if securityContext, ok := manager["securityContext"].(map[string]any); ok {
		readOnlyRoot, _ = securityContext["readOnlyRootFilesystem"].(bool)
	}

	if !readOnlyRoot {
		t.Error("manager container root filesystem is not read-only")
	}

	manifestsPath := ""
	if envs, ok := manager["env"].([]any); ok {
		for _, e := range envs {
			env, ok := e.(map[string]any)
			if !ok {
				continue
			}

			if name, _ := env["name"].(string); name == "ODH_MODULE_OPERATOR_MANIFESTS_PATH" {
				manifestsPath, _ = env["value"].(string)
			}
		}
	}

	if manifestsPath != "/manifests" {
		t.Errorf("ODH_MODULE_OPERATOR_MANIFESTS_PATH = %q, want /manifests", manifestsPath)
	}
}

// TestGeneratedChartDeploymentInventoryHasNoManifestCopying checks the
// committed generated chart inventory: it carries Helm directives, so the
// assertions are structural rather than decoded YAML.
func TestGeneratedChartDeploymentInventoryHasNoManifestCopying(t *testing.T) {
	dep := readFile(t, filepath.Join("..", "..", "config", "chart", "templates", "apps_v1_deployment.yaml"))

	for _, forbidden := range []string{"copy-manifests", "/opt/manifests", "name: manifests"} {
		if strings.Contains(dep, forbidden) {
			t.Errorf("generated deployment inventory still references %q:\n%s", forbidden, dep)
		}
	}

	if !strings.Contains(dep, "value: /manifests") {
		t.Errorf("generated deployment inventory does not read /manifests:\n%s", dep)
	}

	if !strings.Contains(dep, "imagePullPolicy: {{ .Values.image.pullPolicy }}") {
		t.Errorf("generated deployment inventory does not template the pull policy:\n%s", dep)
	}

	if !strings.Contains(dep, "readOnlyRootFilesystem: true") {
		t.Errorf("generated deployment inventory lost the read-only root filesystem:\n%s", dep)
	}
}
