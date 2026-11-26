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

package feastoperator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	fwapi "github.com/opendatahub-io/odh-platform-utilities/framework/api"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions"
	"github.com/opendatahub-io/odh-platform-utilities/framework/controller/actions/status/releases"
	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
)

// Test images used to observe startup image replacement through rendering.
const (
	testOperatorImage = "registry.example.com/feast-operator@sha256:operator"
	testFeatureServer = "registry.example.com/feature-server@sha256:fserver"
	testKubeRBACProxy = "registry.example.com/odh-kube-rbac-proxy@sha256:proxy"
	testOIDCIssuerA   = "https://issuer-a.example.com"
	testOIDCIssuerB   = "https://issuer-b.example.com"
	testInstanceUID   = "feastoperator-render-test-uid"
)

// writeParamsOverlayFixture writes a minimal overlay fixture at
// root/feastoperator/overlays/<overlay> whose rendered output — a ConfigMap
// generated from params.env — exposes every parameter value, so projections
// are observable through real rendering. It returns the overlay directory.
func writeParamsOverlayFixture(t *testing.T, root string, overlay string, paramsEnv string) string {
	t.Helper()

	dir := filepath.Join(root, componentName, overlay)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	kustomization := `apiVersion: kustomize.config.k8s.io/v1beta1
configMapGenerator:
- name: feast-operator-parameters
  envs:
  - params.env
`
	if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(kustomization), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, paramsEnvFile), []byte(paramsEnv), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

// defaultFixtureParamsEnv is the base params.env of test fixtures. Its
// non-image defaults prove the fixture was actually read: a root resolved to
// nowhere would surface as missing keys (or a render error), not these values.
const defaultFixtureParamsEnv = `RELATED_IMAGE_FEAST_OPERATOR=registry.example.com/feast-operator:default
RELATED_IMAGE_FEATURE_SERVER=registry.example.com/feature-server:default
RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE=registry.example.com/odh-kube-rbac-proxy:default
OIDC_ISSUER_URL=
`

// chdir switches the process working directory for the duration of the test.
// Tests using it mutate process state and therefore run sequentially.
func chdir(t *testing.T, dir string) {
	t.Helper()

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatal(err)
		}
	})
}

// newRenderTestFeastOperator returns a FeastOperator with a stable UID and the
// given generation, optionally configured with an OIDC issuer.
func newRenderTestFeastOperator(generation int64, issuerURL string, withOIDC bool) *componentApi.FeastOperator {
	obj := newTestFeastOperator()
	obj.UID = testInstanceUID
	obj.Generation = generation
	if withOIDC {
		obj.Spec.OIDC = &componentApi.GatewayOIDCSpec{IssuerURL: issuerURL}
	}

	return obj
}

// runParamsReconcile drives initialize + setKustomizedParams + the render
// action with a fresh request, mirroring the production action ordering.
func runParamsReconcile(t *testing.T, m *Module, cfg *moduleconfig.Config, renderAction actions.Fn, feast *componentApi.FeastOperator) *fwtypes.ReconciliationRequest {
	t.Helper()

	rr := &fwtypes.ReconciliationRequest{
		Instance: feast,
		Release:  cfg.Release(),
	}

	ctx := context.Background()
	if err := m.initialize(ctx, rr); err != nil {
		t.Fatal(err)
	}
	if err := m.setKustomizedParams(ctx, rr); err != nil {
		t.Fatal(err)
	}
	if err := renderAction(ctx, rr); err != nil {
		t.Fatal(err)
	}

	return rr
}

// renderedParamsData returns the data of the generated
// feast-operator-parameters ConfigMap from a rendered request.
func renderedParamsData(t *testing.T, rr *fwtypes.ReconciliationRequest) map[string]string {
	t.Helper()

	for i := range rr.Resources {
		if rr.Resources[i].GetKind() == "ConfigMap" && strings.HasPrefix(rr.Resources[i].GetName(), "feast-operator-parameters") {
			data, found, err := unstructured.NestedStringMap(rr.Resources[i].Object, "data")
			if err != nil || !found {
				t.Fatalf("failed to read generated ConfigMap data: %v (found=%v)", err, found)
			}

			return data
		}
	}

	t.Fatal("feast-operator-parameters ConfigMap not found in rendered resources")

	return nil
}

// deploymentEnv returns the value of an env var of the named container.
func deploymentEnv(t *testing.T, deploy *appsv1.Deployment, containerName string, key string) string {
	t.Helper()

	for i := range deploy.Spec.Template.Spec.Containers {
		container := &deploy.Spec.Template.Spec.Containers[i]
		if container.Name != containerName {
			continue
		}
		for _, env := range container.Env {
			if env.Name == key {
				return env.Value
			}
		}
	}

	t.Fatalf("env %s of container %s not found", key, containerName)

	return ""
}

// TestSetKustomizedParamsOIDCLifecycleThroughRender covers the runtime OIDC
// lifecycle against a real params.env fixture and the production render
// action: unset -> issuer A -> issuer B -> unset, with static image overrides
// persisting and the base file on disk never changing. One render action is
// reused across reconciles with a stable instance UID and increasing
// generation, so the action's default cache cannot mask stale output.
func TestSetKustomizedParamsOIDCLifecycleThroughRender(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE", testOperatorImage)
	t.Setenv("RELATED_IMAGE_ODH_FEATURE_SERVER_IMAGE", testFeatureServer)
	t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

	manifests := t.TempDir()
	overlayDir := writeParamsOverlayFixture(t, manifests, overlayODH, defaultFixtureParamsEnv)
	diskPath := filepath.Join(overlayDir, paramsEnvFile)
	baseBytes, err := os.ReadFile(diskPath)
	g.Expect(err).NotTo(HaveOccurred())

	cfg := &moduleconfig.Config{
		PlatformType:          moduleconfig.PlatformTypeOpenDataHub,
		PlatformVersion:       "1.0.0",
		ManifestsPath:         manifests,
		ApplicationsNamespace: "test-ns",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())

	renderAction := m.newRenderAction(cfg)

	// One reconcile per lifecycle step; the generation increases so the
	// render cache key changes and the action must re-render each time.
	steps := []struct {
		name       string
		issuerURL  string
		withOIDC   bool
		wantIssuer string
	}{
		{name: "oidc unset", wantIssuer: ""},
		{name: "oidc issuer A", issuerURL: testOIDCIssuerA, withOIDC: true, wantIssuer: testOIDCIssuerA},
		{name: "oidc issuer B", issuerURL: testOIDCIssuerB, withOIDC: true, wantIssuer: testOIDCIssuerB},
		{name: "oidc unset again", wantIssuer: ""},
	}

	generation := int64(0)
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			g := NewWithT(t)

			feast := newRenderTestFeastOperator(generation, step.issuerURL, step.withOIDC)
			rr := runParamsReconcile(t, m, cfg, renderAction, feast)

			// The render actually executed (not satisfied from cache).
			g.Expect(rr.Generated).To(BeTrue())

			data := renderedParamsData(t, rr)
			g.Expect(data["OIDC_ISSUER_URL"]).To(Equal(step.wantIssuer))

			// Static startup image overrides persist across reconciles.
			g.Expect(data["RELATED_IMAGE_FEAST_OPERATOR"]).To(Equal(testOperatorImage))
			g.Expect(data["RELATED_IMAGE_FEATURE_SERVER"]).To(Equal(testFeatureServer))
			g.Expect(data["RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"]).To(Equal(testKubeRBACProxy))

			// The image-baked file on disk stays byte-identical.
			current, err := os.ReadFile(diskPath)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current).To(Equal(baseBytes))
		})

		generation++
	}

	// An invalid issuer still fails validation before any rendering, and
	// leaves the physical manifests untouched.
	t.Run("invalid issuer returns an error", func(t *testing.T) {
		g := NewWithT(t)

		feast := newRenderTestFeastOperator(generation, "not-a-url", true)

		ctx := context.Background()
		rr := &fwtypes.ReconciliationRequest{Instance: feast, Release: cfg.Release()}
		g.Expect(m.initialize(ctx, rr)).To(Succeed())
		err := m.setKustomizedParams(ctx, rr)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("invalid OIDC issuer URL"))

		current, err := os.ReadFile(diskPath)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current).To(Equal(baseBytes))
	})
}

// TestRenderBundledOverlaysThroughModuleFS renders the real bundled overlays
// through the Module filesystem for ODH and RHOAI platform types, asserting
// the operand manager image, feature-server and kube-rbac-proxy image
// parameters, OIDC env, configured namespace, and component labels — while
// the bundled params.env files stay byte-identical on disk.
func TestRenderBundledOverlaysThroughModuleFS(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE", testOperatorImage)
	t.Setenv("RELATED_IMAGE_ODH_FEATURE_SERVER_IMAGE", testFeatureServer)
	t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

	bundledRoot := filepath.Join("..", "..", "..", "config", "manifests")

	// Snapshot the physical params.env files of both overlays; every case
	// must leave them byte-identical.
	originalEnv := map[string][]byte{}
	for _, overlay := range []string{overlayODH, overlayRhoai} {
		data, err := os.ReadFile(filepath.Join(bundledRoot, componentName, overlay, paramsEnvFile))
		g.Expect(err).NotTo(HaveOccurred())
		originalEnv[overlay] = data
	}

	for _, tc := range []struct {
		name              string
		platform          moduleconfig.PlatformType
		overlay           string
		wantServiceMonitr bool
	}{
		{name: "ODH", platform: moduleconfig.PlatformTypeOpenDataHub, overlay: overlayODH, wantServiceMonitr: false},
		{name: "self-managed RHOAI", platform: moduleconfig.PlatformTypeSelfManagedRhoai, overlay: overlayRhoai, wantServiceMonitr: true},
		{name: "managed RHOAI", platform: moduleconfig.PlatformTypeManagedRhoai, overlay: overlayRhoai, wantServiceMonitr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			cfg := &moduleconfig.Config{
				PlatformType:          tc.platform,
				PlatformVersion:       "1.0.0",
				ManifestsPath:         bundledRoot,
				ApplicationsNamespace: "test-ns",
			}

			m, err := NewModule(cfg)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(m.manifestInfo.SourcePath).To(Equal(tc.overlay))

			feast := newRenderTestFeastOperator(1, testOIDCIssuerA, true)
			rr := runParamsReconcile(t, m, cfg, m.newRenderAction(cfg), feast)

			// The operand manager Deployment projects every parameter.
			deploy := &appsv1.Deployment{}
			found := false
			serviceMonitors := 0
			for i := range rr.Resources {
				if rr.Resources[i].GetKind() == "ServiceMonitor" {
					serviceMonitors++
				}
				if rr.Resources[i].GetKind() == "Deployment" && rr.Resources[i].GetName() == deploymentName {
					found = true
					g.Expect(runtime.DefaultUnstructuredConverter.FromUnstructured(rr.Resources[i].Object, deploy)).To(Succeed())
				}
			}
			g.Expect(found).To(BeTrue(), "Deployment %s not rendered", deploymentName)
			g.Expect(serviceMonitors > 0).To(Equal(tc.wantServiceMonitr),
				"ServiceMonitor presence (%d) does not match expectation for overlay %s", serviceMonitors, tc.overlay)

			g.Expect(deploy.Namespace).To(Equal(cfg.ApplicationsNamespace))
			g.Expect(deploy.Labels).To(SatisfyAll(
				HaveKeyWithValue(componentLabelKey, componentLabelValue),
				HaveKeyWithValue(partOfLabelKey, componentName),
			))
			g.Expect(deploy.Spec.Template.Labels).To(SatisfyAll(
				HaveKeyWithValue(componentLabelKey, componentLabelValue),
				HaveKeyWithValue(partOfLabelKey, componentName),
			))

			var managerImage string
			for i := range deploy.Spec.Template.Spec.Containers {
				if deploy.Spec.Template.Spec.Containers[i].Name == "manager" {
					managerImage = deploy.Spec.Template.Spec.Containers[i].Image
				}
			}
			g.Expect(managerImage).To(Equal(testOperatorImage))
			g.Expect(deploymentEnv(t, deploy, "manager", "RELATED_IMAGE_FEATURE_SERVER")).To(Equal(testFeatureServer))
			g.Expect(deploymentEnv(t, deploy, "manager", "RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE")).To(Equal(testKubeRBACProxy))
			g.Expect(deploymentEnv(t, deploy, "manager", ParamsEnvKeyOIDCIssuerURL)).To(Equal(testOIDCIssuerA))

			// No render or parameter write reached the physical overlays.
			for _, overlay := range []string{overlayODH, overlayRhoai} {
				current, err := os.ReadFile(filepath.Join(bundledRoot, componentName, overlay, paramsEnvFile))
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(current).To(Equal(originalEnv[overlay]))
			}
		})
	}
}

// TestManifestRootVariants covers absolute, relative, empty-string, and "."
// configured manifest roots. Every variant must read the same fixture through
// real rendering and project changed values — proving the empty and
// current-directory roots resolve to the working directory instead of
// silently degrading into missing-params no-ops.
func TestManifestRootVariants(t *testing.T) {
	for _, tc := range []struct {
		name          string
		manifestsPath string
		useWorkdir    bool
	}{
		{name: "absolute", manifestsPath: "", useWorkdir: false},
		{name: "relative", manifestsPath: "manifests", useWorkdir: true},
		{name: "empty", manifestsPath: "", useWorkdir: true},
		{name: "dot", manifestsPath: ".", useWorkdir: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

			tmp := t.TempDir()

			// Absolute: the manifests root is the temp dir itself.
			// Workdir-relative variants: chdir into the temp dir; the
			// relative case uses a "manifests" subdirectory, while the
			// empty and "." cases resolve to the working directory.
			manifestsPath := tmp
			if tc.useWorkdir {
				chdir(t, tmp)
				if tc.name == "relative" {
					manifestsPath = "manifests"
				} else {
					manifestsPath = tc.manifestsPath
				}
			}

			overlayDir := writeParamsOverlayFixture(t, manifestsPath, overlayODH, defaultFixtureParamsEnv)
			diskPath := filepath.Join(overlayDir, paramsEnvFile)
			baseBytes, err := os.ReadFile(diskPath)
			g.Expect(err).NotTo(HaveOccurred())

			cfg := &moduleconfig.Config{
				PlatformType:          moduleconfig.PlatformTypeOpenDataHub,
				PlatformVersion:       "1.0.0",
				ManifestsPath:         manifestsPath,
				ApplicationsNamespace: "test-ns",
			}

			m, err := NewModule(cfg)
			g.Expect(err).NotTo(HaveOccurred())

			feast := newRenderTestFeastOperator(1, testOIDCIssuerA, true)
			rr := runParamsReconcile(t, m, cfg, m.newRenderAction(cfg), feast)

			data := renderedParamsData(t, rr)
			// The fixture defaults prove the root was actually read ...
			g.Expect(data["RELATED_IMAGE_FEAST_OPERATOR"]).To(Equal("registry.example.com/feast-operator:default"))
			// ... and the projected values prove writes reached the overlay.
			g.Expect(data["RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"]).To(Equal(testKubeRBACProxy))
			g.Expect(data["OIDC_ISSUER_URL"]).To(Equal(testOIDCIssuerA))

			current, err := os.ReadFile(diskPath)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(current).To(Equal(baseBytes))
		})
	}
}

// TestModuleInstancesAreIsolated verifies that each Module owns an independent
// overlay: parameter writes through one Module are invisible to another, and
// constructing a fresh Module resets the overlay to the base defaults.
func TestModuleInstancesAreIsolated(t *testing.T) {
	g := NewWithT(t)

	t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

	manifests := t.TempDir()
	writeParamsOverlayFixture(t, manifests, overlayODH, defaultFixtureParamsEnv)

	newCfg := func() *moduleconfig.Config {
		return &moduleconfig.Config{
			PlatformType:          moduleconfig.PlatformTypeOpenDataHub,
			PlatformVersion:       "1.0.0",
			ManifestsPath:         manifests,
			ApplicationsNamespace: "test-ns",
		}
	}

	m1, err := NewModule(newCfg())
	g.Expect(err).NotTo(HaveOccurred())

	// Reconcile an OIDC issuer through the first Module.
	feast := newRenderTestFeastOperator(1, testOIDCIssuerA, true)
	rr := runParamsReconcile(t, m1, newCfg(), m1.newRenderAction(newCfg()), feast)
	g.Expect(renderedParamsData(t, rr)["OIDC_ISSUER_URL"]).To(Equal(testOIDCIssuerA))

	// A second Module over the same physical root sees the base defaults,
	// not the first Module's overlay writes.
	m2, err := NewModule(newCfg())
	g.Expect(err).NotTo(HaveOccurred())

	rr2 := runParamsReconcile(t, m2, newCfg(), m2.newRenderAction(newCfg()), newRenderTestFeastOperator(1, "", false))
	data2 := renderedParamsData(t, rr2)
	g.Expect(data2["OIDC_ISSUER_URL"]).To(Equal(""))
	g.Expect(data2["RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE"]).To(Equal(testKubeRBACProxy))

	// The first Module still observes its own overlay.
	rr1 := runParamsReconcile(t, m1, newCfg(), m1.newRenderAction(newCfg()), newRenderTestFeastOperator(2, testOIDCIssuerB, true))
	g.Expect(renderedParamsData(t, rr1)["OIDC_ISSUER_URL"]).To(Equal(testOIDCIssuerB))
}

// TestMissingParamsAreNoOps preserves the no-op semantics of parameter
// application: a missing params.env file is skipped entirely, and image
// replacement never adds keys that the file does not already define.
func TestMissingParamsAreNoOps(t *testing.T) {
	t.Run("params.env file absent", func(t *testing.T) {
		g := NewWithT(t)

		t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

		manifests := t.TempDir()
		dir := filepath.Join(manifests, componentName, overlayODH)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		kustomization := `apiVersion: kustomize.config.k8s.io/v1beta1
resources:
- configmap.yaml
`
		if err := os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(kustomization), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "configmap.yaml"), []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: plain-cm\ndata:\n  key: value\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		cfg := &moduleconfig.Config{
			PlatformType:          moduleconfig.PlatformTypeOpenDataHub,
			PlatformVersion:       "1.0.0",
			ManifestsPath:         manifests,
			ApplicationsNamespace: "test-ns",
		}

		// Neither startup nor per-reconcile application errors on the
		// missing file; rendering still succeeds.
		m, err := NewModule(cfg)
		g.Expect(err).NotTo(HaveOccurred())

		feast := newRenderTestFeastOperator(1, testOIDCIssuerA, true)
		rr := runParamsReconcile(t, m, cfg, m.newRenderAction(cfg), feast)

		found := false
		for i := range rr.Resources {
			if rr.Resources[i].GetKind() == "ConfigMap" && rr.Resources[i].GetName() == "plain-cm" {
				found = true
			}
		}
		g.Expect(found).To(BeTrue(), "plain ConfigMap not rendered")
	})

	t.Run("image key absent from params.env", func(t *testing.T) {
		g := NewWithT(t)

		t.Setenv("RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE", testKubeRBACProxy)

		// The file does not define the proxy image key: Replacement must
		// skip it, leaving the file content unchanged.
		paramsEnv := "RELATED_IMAGE_FEAST_OPERATOR=registry.example.com/feast-operator:default\nOIDC_ISSUER_URL=\n"
		manifests := t.TempDir()
		writeParamsOverlayFixture(t, manifests, overlayODH, paramsEnv)

		m, err := NewModule(&moduleconfig.Config{
			PlatformType:  moduleconfig.PlatformTypeOpenDataHub,
			ManifestsPath: manifests,
		})
		g.Expect(err).NotTo(HaveOccurred())

		content, err := m.kustomizeFS.ReadFile(manifestParamsEnvPath(m.manifestInfo))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(string(content)).To(Equal(paramsEnv))
	})
}

// TestReadOnlyManifestsBaseRejectsWrites proves the read-only base protection
// is explicit in the construction: writes through the rooted base fail
// regardless of filesystem permissions (which can be bypassed when running
// as root).
func TestReadOnlyManifestsBaseRejectsWrites(t *testing.T) {
	g := NewWithT(t)

	dir := t.TempDir()
	file := filepath.Join(dir, paramsEnvFile)
	if err := os.WriteFile(file, []byte("key=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	base, err := newReadOnlyManifestsBase(dir)
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(base.WriteFile(paramsEnvFile, []byte("key=changed\n"))).To(HaveOccurred())
	g.Expect(base.MkdirAll("subdir")).To(HaveOccurred())
	g.Expect(base.RemoveAll(paramsEnvFile)).To(HaveOccurred())

	// Reads still work through the read-only base.
	content, err := base.ReadFile(paramsEnvFile)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(string(content)).To(Equal("key=value\n"))
}

// TestAbsoluteManifestsRoot covers the manifests-root normalization: empty
// and "." resolve to the working directory, relative paths resolve against
// it, and absolute paths are passed through cleaned.
func TestAbsoluteManifestsRoot(t *testing.T) {
	g := NewWithT(t)

	wd, err := os.Getwd()
	g.Expect(err).NotTo(HaveOccurred())

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty", input: "", want: filepath.Clean(wd)},
		{name: "dot", input: ".", want: filepath.Clean(wd)},
		{name: "relative", input: filepath.Join("some", "dir"), want: filepath.Join(wd, "some", "dir")},
		{name: "absolute", input: filepath.Join(string(filepath.Separator), "manifests"), want: filepath.Join(string(filepath.Separator), "manifests")},
		{name: "absolute unclean", input: filepath.Join(string(filepath.Separator), "manifests", "..", "manifests"), want: filepath.Join(string(filepath.Separator), "manifests")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			got, err := absoluteManifestsRoot(tc.input)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(got).To(Equal(tc.want))
		})
	}
}

// TestReleasesMetadataLoadsFromPhysicalRoot verifies component metadata still
// loads from an absolute physical fixture root through the framework releases
// action (the manager contract feeds rr.ManifestsBasePath), while the render
// descriptors stay relative to the module filesystem.
func TestReleasesMetadataLoadsFromPhysicalRoot(t *testing.T) {
	g := NewWithT(t)

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, componentName), 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := `releases:
  - name: Feast
    version: 9.9.9
`
	if err := os.WriteFile(filepath.Join(root, componentName, "component_metadata.yaml"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &moduleconfig.Config{
		PlatformType:          moduleconfig.PlatformTypeOpenDataHub,
		PlatformVersion:       "1.0.0",
		ManifestsPath:         root,
		ApplicationsNamespace: "test-ns",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())

	// Render descriptors remain filesystem-relative.
	g.Expect(m.manifestInfo.Path).To(BeEmpty())
	g.Expect(m.manifestInfo.ContextDir).To(Equal(componentName))

	obj := newTestFeastOperator()
	obj.TypeMeta = metav1.TypeMeta{
		APIVersion: "components.platform.opendatahub.io/v1alpha1",
		Kind:       "FeastOperator",
	}

	// The releases action addresses metadata physically, through
	// rr.ManifestsBasePath, exactly as the framework manager provides it.
	rr := &fwtypes.ReconciliationRequest{
		Instance:          obj,
		ManifestsBasePath: root,
	}

	g.Expect(releases.NewAction()(context.Background(), rr)).To(Succeed())
	g.Expect(obj.GetReleaseStatus().Releases).To(Equal([]fwapi.ComponentRelease{
		{Name: "Feast", Version: "9.9.9"},
	}))
}
