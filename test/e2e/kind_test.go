//go:build e2e

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

package e2e

import (
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	feastoperatorcontroller "github.com/opendatahub-io/feast-module-operator/internal/controller/feastoperator"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

// Kind-only e2e mode. The aggregate Kind workflow (hack/scripts/
// test-e2e-kind.sh via `make test-e2e ODH_PLATFORM_TYPE=...`) enables this
// mode and supplies every input; a standalone `make test-e2e-run` keeps
// E2E_KIND_CHECKS=false and the existing core suite.
const (
	kindChecksEnv         = "E2E_KIND_CHECKS"
	kindClusterEnv        = "E2E_KIND_CLUSTER"
	expectedFeastImageEnv = "E2E_EXPECTED_FEAST_IMAGE"

	// fixtureOwnerAnnotation marks test-owned fixtures (FeastOperator
	// singleton, odh-data-config platformVersion key, dedicated cluster
	// namespace). Its value is the dedicated Kind cluster name.
	fixtureOwnerAnnotation = "e2e.feast.dev/owner"

	// ConfigMap fixture metadata recording the platformVersion key's prior
	// state so a successful run restores exactly what it changed and a new
	// authorized run can recover a failed run's leftover state.
	cmPriorPresentAnnotation = "e2e.feast.dev/platformVersion.prior-present"
	cmPriorValueAnnotation   = "e2e.feast.dev/platformVersion.prior-value"

	// Module deployment shape expected by the Kind workflow: manifests are
	// read from the image-baked directory on a read-only root filesystem,
	// with no copy-manifests init container and no forced image pull for
	// the locally loaded image.
	manifestsPathEnvName = "ODH_MODULE_OPERATOR_MANIFESTS_PATH"
	manifestsPathValue   = "/manifests"
	localImagePullPolicy = "Never"

	// feastOperatorImageParamEnv is the env var on the module Deployment
	// (injectable through the chart's extraEnvs) that the controller maps
	// onto the params.env RELATED_IMAGE_FEAST_OPERATOR key.
	feastOperatorImageParamEnv = "RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE"

	testOIDCIssuerURLUpdate = "https://e2e-oidc.example.com/realms/test-2"

	// Bounded budgets for Kind-only behavior: finalization of a
	// terminating suite-owned singleton, and a full module pod restart
	// with re-reconciliation.
	terminatingTimeout = 5 * time.Minute
	restartTimeout     = 3 * time.Minute
)

func kindChecksEnabled() bool {
	return os.Getenv(kindChecksEnv) == "true"
}

func kindClusterName() string {
	return os.Getenv(kindClusterEnv)
}

func expectedFeastImage() string {
	return os.Getenv(expectedFeastImageEnv)
}

// requireKindInputs fails the run before any cluster interaction when Kind
// mode is enabled but its required inputs are missing or invalid. Kind mode
// never silently degrades to skips.
func requireKindInputs(t *testing.T) {
	t.Helper()

	if cluster := kindClusterName(); cluster == "" {
		t.Fatalf("%s=true requires %s to name the dedicated Kind cluster", kindChecksEnv, kindClusterEnv)
	}

	expected := expectedFeastImage()
	if expected == "" {
		t.Fatalf("%s=true requires %s to carry the verified operand image override", kindChecksEnv, expectedFeastImageEnv)
	}

	// The expected image must be a digest reference for the same approved
	// Feast release, textually distinct from every bundled overlay tag:
	// equality with the bundled default would not prove substitution.
	for _, overlay := range []string{
		feastoperatorcontroller.OverlayFor(moduleconfig.PlatformTypeOpenDataHub),
		feastoperatorcontroller.OverlayFor(moduleconfig.PlatformTypeSelfManagedRhoai),
	} {
		bundled := support.OverlayParam(t, overlay, "RELATED_IMAGE_FEAST_OPERATOR")
		if expected == bundled {
			t.Fatalf("%s must differ textually from the bundled %s value %q",
				expectedFeastImageEnv, overlay, bundled)
		}
	}
}

// verifyKindClusterIdentity fails the run before any fixture interaction
// when the connected cluster is not the named dedicated Kind cluster.
// Kind names its primary control-plane node exactly
// <cluster>-control-plane (additional nodes carry numeric suffixes), so
// the exact name identifies the cluster the kubeconfig points at without
// accepting a different cluster whose name merely shares a prefix. This
// guards the standalone opt-in path (E2E_KIND_CHECKS=true without the
// orchestration script) against an ambient or in-cluster configuration:
// the suite's fixture operations must never run against a cluster other
// than the named dedicated one.
func verifyKindClusterIdentity(t *testing.T) {
	t.Helper()

	nodes := &corev1.NodeList{}
	if err := k8sClient.List(ctx, nodes); err != nil {
		t.Fatalf("kind mode requires cluster access to verify the dedicated cluster identity: %v", err)
	}
	expectedNode := kindClusterName() + "-control-plane"
	names := make([]string, 0, len(nodes.Items))
	for i := range nodes.Items {
		names = append(names, nodes.Items[i].Name)
		if nodes.Items[i].Name == expectedNode {
			return
		}
	}
	t.Fatalf("connected cluster is not the dedicated Kind cluster %q (expected node %q; nodes: %s); refusing Kind-mode fixture operations against it",
		kindClusterName(), expectedNode, strings.Join(names, ", "))
}

// kindSubtest wraps a Kind-only assertion so standalone runs explicitly
// report the disabled assertion instead of silently dropping it.
func (ft *foundationTests) kindSubtest(test func(*testing.T)) func(*testing.T) {
	return func(t *testing.T) {
		if !kindChecksEnabled() {
			t.Skipf("%s=%q: Kind-only assertion disabled in standalone mode; opt in with %s=true, %s, and %s",
				kindChecksEnv, os.Getenv(kindChecksEnv), kindChecksEnv, kindClusterEnv, expectedFeastImageEnv)
			return
		}
		requireKindInputs(t)
		test(t)
	}
}

// testKindModuleDeploymentShape verifies the deployment shape produced by
// the shared copy-on-write manifest filesystem: the module Deployment and
// its running Pod carry no copy-manifests init container, no manifests
// emptyDir volume or mount, a read-only manager root filesystem, the baked
// /manifests path, and the no-forced-pull policy used for the locally
// loaded image.
func (ft *foundationTests) testKindModuleDeploymentShape(t *testing.T) {
	g := NewWithT(t)

	deploy := ft.operatorDeploy.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())

	expectModuleContainerShape(t, deploy.Spec.Template.Spec, deploy.Spec.Template.Spec.Containers, "module Deployment")
	g.Expect(envVarValue(deploy, feastOperatorImageParamEnv)).To(Equal(expectedFeastImage()),
		"module Deployment must carry the %s override injected through the chart", feastOperatorImageParamEnv)

	pods := listPodsForDeployment(t, deploy)
	pod := &pods.Items[0]
	expectModuleContainerShape(t, pod.Spec, pod.Spec.Containers, "module Pod")

	// Container identity note: the container runtime's reported
	// status.containerStatuses[].image is a store name for the image ID,
	// which is not necessarily the requested reference when several tags
	// share one ID (each workflow run rebuilds the same content under a
	// new tag). The pod SPEC image is the authoritative record of what
	// the kubelet was asked to run.
	podManager, found := containerByName(pod.Spec.Containers, "manager")
	g.Expect(found).To(BeTrue(), "module Pod must run the manager container")
	deployManager, _ := containerByName(deploy.Spec.Template.Spec.Containers, "manager")
	g.Expect(podManager.Image).To(Equal(deployManager.Image),
		"module Pod must run the deployment image")

	for i := range pod.Status.ContainerStatuses {
		cs := &pod.Status.ContainerStatuses[i]
		if cs.Name != "manager" {
			continue
		}
		g.Expect(cs.ImageID).NotTo(BeEmpty(),
			"module manager container must have a resolved image")
		g.Expect(cs.Ready).To(BeTrue(), "module manager container must be ready")
	}
}

// expectModuleContainerShape asserts the no-copy-manifests container shape
// against a pod spec.
func expectModuleContainerShape(t *testing.T, spec corev1.PodSpec, containers []corev1.Container, what string) {
	t.Helper()
	g := NewWithT(t)

	g.Expect(containers).NotTo(BeEmpty(), "%s must run containers", what)
	g.Expect(spec.InitContainers).To(BeEmpty(), "%s must not run init containers (copy-manifests removed)", what)

	manager, found := containerByName(containers, "manager")
	g.Expect(found).To(BeTrue(), "%s must run the manager container", what)

	for _, volume := range spec.Volumes {
		g.Expect(volume.EmptyDir).To(BeNil(), "%s must not carry writable volumes (%s)", what, volume.Name)
	}
	for _, mount := range manager.VolumeMounts {
		g.Expect(mount.Name).NotTo(Equal("manifests"), "%s must not mount a manifests volume", what)
	}

	g.Expect(manager.SecurityContext).NotTo(BeNil(), "%s manager must set a security context", what)
	g.Expect(manager.SecurityContext.ReadOnlyRootFilesystem).NotTo(BeNil(), "%s manager must set readOnlyRootFilesystem", what)
	g.Expect(*manager.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue(), "%s manager root filesystem must be read-only", what)

	g.Expect(containerEnvValue(manager, manifestsPathEnvName)).To(Equal(manifestsPathValue),
		"%s manager must read manifests from the baked path", what)
	g.Expect(string(manager.ImagePullPolicy)).To(Equal(localImagePullPolicy),
		"%s manager must not force image pulls in the Kind workflow", what)
}

// testKindImageSubstitution verifies static image substitution through the
// RELATED_IMAGE mapping: the digest reference injected via the chart's
// extraEnvs reaches the operand Deployment's manager image after a
// genuinely new module Pod started (each workflow run builds a unique
// local image tag and redeploys).
func (ft *foundationTests) testKindImageSubstitution(t *testing.T) {
	g := NewWithT(t)

	expected := expectedFeastImage()

	// The substituted value must not be the bundled overlay tag.
	deploy := ft.operatorDeploy.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())
	platformType := envVarValue(deploy, platformTypeEnvVar)
	if platformType == "" {
		platformType = string(moduleconfig.DefaultPlatformType)
	}
	bundled := support.OverlayParam(t,
		feastoperatorcontroller.OverlayFor(moduleconfig.ParsePlatformType(platformType)),
		"RELATED_IMAGE_FEAST_OPERATOR")
	g.Expect(expected).NotTo(Equal(bundled),
		"expected Feast image must differ from the bundled overlay value")

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Match(`.status.observedGeneration >= 1`),
		jq.Matchf(`.spec.template.spec.containers[] | select(.name == "manager") | .image == "%s"`, expected),
	))

	// The running operand Pods use the substituted image identity: the pod
	// spec image is the requested reference, and the runtime's imageID
	// carries the resolved digest (the status image field may report a
	// different store name for the same image ID).
	workload := ft.workloadDeploy.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(workload), workload)).To(Succeed())
	pods := listPodsForDeployment(t, workload)
	digest := expected[strings.LastIndex(expected, "@"):]
	for i := range pods.Items {
		podManager, found := containerByName(pods.Items[i].Spec.Containers, "manager")
		g.Expect(found).To(BeTrue(), "operand pod %s must run the manager container", pods.Items[i].Name)
		g.Expect(podManager.Image).To(Equal(expected),
			"operand pod %s must run the substituted Feast image", pods.Items[i].Name)
		for j := range pods.Items[i].Status.ContainerStatuses {
			cs := &pods.Items[i].Status.ContainerStatuses[j]
			if cs.Name == "manager" {
				g.Expect(cs.ImageID).To(ContainSubstring(digest),
					"operand pod %s manager must run the substituted Feast image digest", pods.Items[i].Name)
			}
		}
	}
}

// testKindModuleRestartRetainsState deletes the module Pods and verifies a
// genuinely new module Pod re-applies the image replacements and the live
// CR OIDC value from the image-baked manifests, without writable manifest
// storage.
func (ft *foundationTests) testKindModuleRestartRetainsState(t *testing.T) {
	g := NewWithT(t)

	expected := expectedFeastImage()

	// Project a live OIDC value so retention after restart is observable.
	current := ft.module.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(current), current)).To(Succeed())
	originalOIDC := current.Spec.OIDC.DeepCopy()

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("preserving failed OIDC projection state for diagnostics")
			return
		}
		latest := ft.module.DeepCopy()
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(latest), latest); err != nil {
			return
		}
		restored := latest.DeepCopy()
		restored.Spec.OIDC = originalOIDC
		_ = k8sClient.Patch(ctx, restored, client.MergeFrom(latest))
	})

	if current.Spec.OIDC == nil || current.Spec.OIDC.IssuerURL != testOIDCIssuerURL {
		patched := current.DeepCopy()
		patched.Spec.OIDC = &componentsv1alpha1.GatewayOIDCSpec{IssuerURL: testOIDCIssuerURL}
		g.Expect(k8sClient.Patch(ctx, patched, client.MergeFrom(current))).To(Succeed())
	}
	ft.waitOIDCProjected(t, testOIDCIssuerURL)

	// Restart the module: delete every Pod of the module Deployment.
	deploy := ft.operatorDeploy.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(deploy), deploy)).To(Succeed())

	pods := listPodsForDeployment(t, deploy)
	oldNames := make(map[string]struct{}, len(pods.Items))
	for i := range pods.Items {
		oldNames[pods.Items[i].Name] = struct{}{}
		g.Expect(k8sClient.Delete(ctx, &pods.Items[i])).To(Succeed())
	}
	t.Logf("deleted %d module pod(s) to restart the module", len(pods.Items))

	// A genuinely new module Pod becomes ready.
	g.Eventually(func(g Gomega) {
		fresh := &corev1.PodList{}
		g.Expect(k8sClient.List(ctx, fresh,
			client.InNamespace(ft.operatorNamespace),
			client.MatchingLabels(deploy.Spec.Selector.MatchLabels),
		)).To(Succeed())
		ready := 0
		for i := range fresh.Items {
			pod := &fresh.Items[i]
			if pod.GetDeletionTimestamp() != nil {
				continue
			}
			if _, old := oldNames[pod.Name]; old {
				continue
			}
			for j := range pod.Status.ContainerStatuses {
				if pod.Status.ContainerStatuses[j].Name == "manager" && pod.Status.ContainerStatuses[j].Ready {
					ready++
				}
			}
		}
		g.Expect(ready).To(BeNumerically(">=", 1), "expected a new ready manager pod after the restart")
	}).WithContext(ctx).WithTimeout(restartTimeout).WithPolling(interval).Should(Succeed())

	// The restarted module reconciles from the baked manifests: status is
	// current and Ready again.
	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(restartTimeout).WithPolling(interval).Should(And(
		jq.Match(`.status.phase == "Ready"`),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
	))

	// Image replacement and the live OIDC value are retained.
	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Matchf(`.spec.template.spec.containers[] | select(.name == "manager") | .image == "%s"`, expected),
		jq.Matchf(`.spec.template.spec.containers[] | select(.name == "manager") | .env[] | select(.name == "%s") | .value == "%s"`,
			oidcIssuerEnvName, testOIDCIssuerURL),
	))

	// A healthy fresh module Pod alone does not prove the restarted
	// process re-rendered the baked manifests: the assertions above
	// inspect persisted state that already held before the restart, and
	// a fresh Pod that never reconciled would still satisfy them. Force
	// an observable reconciliation by deleting the operand Deployment:
	// only the restarted module can recreate it, so a fresh Deployment
	// (new UID) that becomes ready running the substituted image with
	// the live OIDC value proves fresh-process rendering from the
	// image-baked manifests, without writable manifest storage.
	stale := ft.workloadDeploy.DeepCopy()
	g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(stale), stale)).To(Succeed())
	staleUID := stale.UID
	g.Expect(k8sClient.Delete(ctx, stale)).To(Succeed())
	t.Log("deleted operand Deployment to force reconciliation by the restarted module")

	g.Eventually(func(g Gomega) {
		fresh := ft.workloadDeploy.DeepCopy()
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(fresh), fresh)).To(Succeed())
		g.Expect(fresh.UID).NotTo(Equal(staleUID),
			"operand Deployment must be recreated by the restarted module")
		g.Expect(fresh.Status.ReadyReplicas).To(BeNumerically(">=", 1),
			"recreated operand Deployment must become ready")
		manager, ok := containerByName(fresh.Spec.Template.Spec.Containers, "manager")
		g.Expect(ok).To(BeTrue(), "recreated operand Deployment must run the manager container")
		g.Expect(manager.Image).To(Equal(expected),
			"recreated operand Deployment must run the substituted Feast image")
		issuer, issuerFound := "", false
		for i := range manager.Env {
			if manager.Env[i].Name == oidcIssuerEnvName {
				issuerFound, issuer = true, manager.Env[i].Value
			}
		}
		g.Expect(issuerFound).To(BeTrue(),
			"recreated operand Deployment must retain the OIDC issuer env")
		g.Expect(issuer).To(Equal(testOIDCIssuerURL),
			"recreated operand Deployment must retain the live OIDC issuer")
	}).WithContext(ctx).WithTimeout(restartTimeout).WithPolling(interval).Should(Succeed())
}

// waitOIDCProjected waits for the module to observe the CR generation and
// project the issuer URL onto the operand manager container.
func (ft *foundationTests) waitOIDCProjected(t *testing.T, issuerURL string) {
	t.Helper()
	g := NewWithT(t)

	g.Eventually(k.Get(ft.module)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(And(
		jq.Matchf(`.spec.oidc.issuerURL == "%s"`, issuerURL),
		jq.Match(`.status.observedGeneration == .metadata.generation`),
		jq.Match(`.status.conditions[] | select(.type == "Ready") | .status == "True"`),
	))

	g.Eventually(k.Get(ft.workloadDeploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Matchf(`.spec.template.spec.containers[] | select(.name == "manager") | .env[] | select(.name == "%s") | .value == "%s"`,
			oidcIssuerEnvName, issuerURL),
	)
}

// ---------------------------------------------------------------------------
// platformVersion fixture lifecycle (odh-data-config)
// ---------------------------------------------------------------------------

// recoverPlatformVersionFixture restores the platformVersion key state
// recorded by a previous failed run, before the fresh-chart assertion
// checks it. It fails closed on missing, corrupt, or conflicting
// ownership: an arbitrary nonempty platform value is never erased.
func (ft *foundationTests) recoverPlatformVersionFixture(t *testing.T) {
	t.Helper()
	g := NewWithT(t)

	key := feastoperatorcontroller.PlatformVersionKey
	cm := &corev1.ConfigMap{}
	err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      feastoperatorcontroller.PlatformConfigCMName,
		Namespace: ft.operatorNamespace,
	}, cm)
	switch {
	case k8serr.IsNotFound(err):
		return
	case err != nil:
		t.Fatalf("failed to get ConfigMap %s: %v", feastoperatorcontroller.PlatformConfigCMName, err)
	}

	if configMapOwnedByPlatform(cm) {
		return
	}

	value, has := cm.Data[key]
	marked := cm.Annotations[fixtureOwnerAnnotation] != ""
	if !has || value == "" {
		if marked {
			t.Fatalf("corrupt fixture metadata on %s: owner marker present but %s is unset",
				feastoperatorcontroller.PlatformConfigCMName, key)
		}
		return
	}

	owner := cm.Annotations[fixtureOwnerAnnotation]
	if owner == "" {
		t.Fatalf("%s carries %s=%q without a fixture owner marker; refusing to modify a value this suite does not own",
			feastoperatorcontroller.PlatformConfigCMName, key, value)
	}
	if owner != kindClusterName() {
		t.Fatalf("%s fixture is owned by %q (expected cluster %q); refusing to recover it",
			feastoperatorcontroller.PlatformConfigCMName, owner, kindClusterName())
	}

	switch present := cm.Annotations[cmPriorPresentAnnotation]; present {
	case "true":
		prior, ok := cm.Annotations[cmPriorValueAnnotation]
		if !ok {
			t.Fatalf("corrupt fixture metadata on %s: %s=%q without a recorded prior value",
				feastoperatorcontroller.PlatformConfigCMName, cmPriorPresentAnnotation, present)
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[key] = prior
	case "false":
		delete(cm.Data, key)
	default:
		t.Fatalf("corrupt fixture metadata on %s: %s=%q (expected \"true\" or \"false\")",
			feastoperatorcontroller.PlatformConfigCMName, cmPriorPresentAnnotation, present)
	}

	delete(cm.Annotations, fixtureOwnerAnnotation)
	delete(cm.Annotations, cmPriorPresentAnnotation)
	delete(cm.Annotations, cmPriorValueAnnotation)

	g.Expect(k8sClient.Update(ctx, cm)).To(Succeed())
	t.Logf("recovered %s %s state left by a failed run (owner %q)",
		feastoperatorcontroller.PlatformConfigCMName, key, owner)
}

// recordPlatformVersionFixture records the platformVersion key's prior
// state, marks the fixture as owned by this suite run, writes the test
// platform version, and registers a failure-aware restore on the root
// test: a successful suite restores the prior state and removes only the
// fixture markers; a failed suite preserves the marked state for
// inspection and later recovery.
func (ft *foundationTests) recordPlatformVersionFixture(t *testing.T, cm *corev1.ConfigMap) {
	t.Helper()
	g := NewWithT(t)

	key := feastoperatorcontroller.PlatformVersionKey
	if value, has := cm.Data[key]; has && value != "" {
		t.Fatalf("%s already carries %s=%q; refusing to overwrite an unowned value",
			feastoperatorcontroller.PlatformConfigCMName, key, value)
	}

	priorValue, priorPresent := cm.Data[key]

	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	cm.Annotations[fixtureOwnerAnnotation] = kindClusterName()
	cm.Annotations[cmPriorPresentAnnotation] = strconv.FormatBool(priorPresent)
	if priorPresent {
		cm.Annotations[cmPriorValueAnnotation] = priorValue
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[key] = e2ePlatformVersion
	g.Expect(k8sClient.Update(ctx, cm)).To(Succeed())
	t.Logf("recorded %s %s fixture state (owner %q, prior present=%t)",
		feastoperatorcontroller.PlatformConfigCMName, key, kindClusterName(), priorPresent)

	root := ft.rootT
	root.Cleanup(func() {
		if root.Failed() {
			root.Logf("preserving marked %s %s fixture state after failed run",
				feastoperatorcontroller.PlatformConfigCMName, key)
			return
		}

		fresh := &corev1.ConfigMap{}
		if err := k8sClient.Get(ctx, client.ObjectKey{
			Name:      feastoperatorcontroller.PlatformConfigCMName,
			Namespace: ft.operatorNamespace,
		}, fresh); err != nil {
			root.Logf("could not re-read %s to restore the fixture: %v",
				feastoperatorcontroller.PlatformConfigCMName, err)
			return
		}
		if fresh.Annotations[fixtureOwnerAnnotation] != kindClusterName() {
			root.Logf("%s fixture owner changed (%q); not restoring",
				feastoperatorcontroller.PlatformConfigCMName, fresh.Annotations[fixtureOwnerAnnotation])
			return
		}
		if priorPresent {
			if fresh.Data == nil {
				fresh.Data = map[string]string{}
			}
			fresh.Data[key] = priorValue
		} else {
			delete(fresh.Data, key)
		}
		delete(fresh.Annotations, fixtureOwnerAnnotation)
		delete(fresh.Annotations, cmPriorPresentAnnotation)
		delete(fresh.Annotations, cmPriorValueAnnotation)
		if err := k8sClient.Update(ctx, fresh); err != nil {
			root.Logf("failed to restore the %s fixture: %v",
				feastoperatorcontroller.PlatformConfigCMName, err)
		}
	})
}

// ---------------------------------------------------------------------------
// failure diagnostics
// ---------------------------------------------------------------------------

// registerKindDiagnostics collects non-secret evidence (events, pod
// status, image identity, manager logs, module status) when the suite
// fails, before any failure-aware cleanup decides what to preserve. It is
// registered after the singleton cleanup so it runs first.
func (ft *foundationTests) registerKindDiagnostics(t *testing.T) {
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}

		t.Logf("kind e2e failed: preserving fixture state for inspection (FeastOperator singleton, %s markers, operand resources)",
			feastoperatorcontroller.PlatformConfigCMName)

		// Module status.
		module := &componentsv1alpha1.FeastOperator{
			ObjectMeta: metav1.ObjectMeta{Name: componentsv1alpha1.FeastOperatorInstanceName},
		}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(module), module); err == nil {
			t.Logf("FeastOperator %s: phase=%s observedGeneration=%d",
				module.Name, module.Status.Phase, module.Status.ObservedGeneration)
			for _, condition := range module.Status.Conditions {
				t.Logf("  condition %s=%s reason=%s message=%q",
					condition.Type, condition.Status, condition.Reason, condition.Message)
			}
		} else {
			t.Logf("FeastOperator %s could not be read: %v", module.Name, err)
		}

		// Deployments.
		deploys := &appsv1.DeploymentList{}
		if err := k8sClient.List(ctx, deploys, client.InNamespace(ft.operatorNamespace)); err == nil {
			for i := range deploys.Items {
				d := &deploys.Items[i]
				image := ""
				if len(d.Spec.Template.Spec.Containers) > 0 {
					image = d.Spec.Template.Spec.Containers[0].Image
				}
				t.Logf("deployment %s: ready=%d/%d image=%s",
					d.Name, d.Status.ReadyReplicas, *d.Spec.Replicas, image)
			}
		}

		// Pods with container image identity.
		pods := &corev1.PodList{}
		if err := k8sClient.List(ctx, pods, client.InNamespace(ft.operatorNamespace)); err == nil {
			for i := range pods.Items {
				pod := &pods.Items[i]
				t.Logf("pod %s: phase=%s", pod.Name, pod.Status.Phase)
				for j := range pod.Status.ContainerStatuses {
					cs := &pod.Status.ContainerStatuses[j]
					t.Logf("  container %s: image=%s imageID=%s ready=%t restarts=%d",
						cs.Name, cs.Image, cs.ImageID, cs.Ready, cs.RestartCount)
				}
			}
		}

		// Events (bounded).
		events := &corev1.EventList{}
		if err := k8sClient.List(ctx, events, client.InNamespace(ft.operatorNamespace)); err == nil {
			limit := len(events.Items)
			if limit > 60 {
				limit = 60
			}
			for _, event := range events.Items[:limit] {
				t.Logf("event %s/%s: %s",
					event.InvolvedObject.Kind, event.InvolvedObject.Name, event.Message)
			}
		}

		// Manager logs from the module pods (bounded tail).
		if kubeClientSet == nil {
			return
		}
		selector := client.MatchingLabels{labelAppName: operatorDeployName}
		modulePods := &corev1.PodList{}
		if err := k8sClient.List(ctx, modulePods,
			client.InNamespace(ft.operatorNamespace), selector); err != nil {
			return
		}
		tail := int64(200)
		for i := range modulePods.Items {
			pod := &modulePods.Items[i]
			stream, err := kubeClientSet.CoreV1().Pods(pod.Namespace).
				GetLogs(pod.Name, &corev1.PodLogOptions{Container: "manager", TailLines: &tail}).Stream(ctx)
			if err != nil {
				t.Logf("manager logs of %s unavailable: %v", pod.Name, err)
				continue
			}
			data, err := io.ReadAll(stream)
			_ = stream.Close()
			if err != nil {
				t.Logf("manager logs of %s unreadable: %v", pod.Name, err)
				continue
			}
			t.Logf("manager logs (tail %d lines) of %s:\n%s", tail, pod.Name, string(data))
		}
	})
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func containerByName(containers []corev1.Container, name string) (corev1.Container, bool) {
	for i := range containers {
		if containers[i].Name == name {
			return containers[i], true
		}
	}
	return corev1.Container{}, false
}

func containerEnvValue(container corev1.Container, name string) string {
	for _, env := range container.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

func listPodsForDeployment(t *testing.T, deploy *appsv1.Deployment) *corev1.PodList {
	t.Helper()

	pods := &corev1.PodList{}
	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		pods.Items = nil
		g.Expect(k8sClient.List(ctx, pods,
			client.InNamespace(deploy.Namespace),
			client.MatchingLabels(deploy.Spec.Selector.MatchLabels),
		)).To(Succeed())
		g.Expect(pods.Items).NotTo(BeEmpty(),
			"expected pods for deployment %s/%s", deploy.Namespace, deploy.Name)
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
	return pods
}
