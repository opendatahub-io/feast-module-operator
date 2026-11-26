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
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"
	k8sm "github.com/lburgazzoli/gomega-matchers/pkg/matchers/k8s"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/test/support"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	labelPartOf            = "platform.opendatahub.io/part-of"
	annotationInstanceName = "platform.opendatahub.io/instance.name"
	annotationInstanceUID  = "platform.opendatahub.io/instance.uid"
	annotationType         = "platform.opendatahub.io/type"
	annotationVersion      = "platform.opendatahub.io/version"

	moduleCRDName = "feastoperators.components.platform.opendatahub.io"

	// platformTypeEnvVar is the env var that carries the platform type on the
	// manager Deployment (bound to the platform-type config key), injectable
	// through the chart's extraEnvs knob.
	platformTypeEnvVar = "ODH_MODULE_OPERATOR_PLATFORM_TYPE"

	operatorDeployName         = "opendatahub-feast-operator"
	workloadDeployName         = "feast-operator-controller-manager"
	workloadMetricsServiceName = "feast-operator-controller-manager-metrics-service"

	odhComponentLabel = "app.opendatahub.io/feastoperator"
	labelAppName      = "app.kubernetes.io/name"
	labelK8sPartOf    = "app.kubernetes.io/part-of"
	labelControlPlane = "control-plane"

	oidcIssuerEnvName = "OIDC_ISSUER_URL"
	testOIDCIssuerURL = "https://e2e-oidc.example.com/realms/test"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	k8sClient client.Client
	k         *k8sm.Operations

	// kubeClientSet serves pod log collection for failure diagnostics.
	kubeClientSet *kubernetes.Clientset

	testScheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(testScheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(testScheme))
}

func TestMain(m *testing.M) {
	os.Exit(runTestMain(m))
}

func runTestMain(m *testing.M) int {
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		return 1
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		return 1
	}

	kubeClientSet, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create clientset: %v\n", err)
		return 1
	}

	k = k8sm.Using(k8sClient)

	return m.Run()
}

type feastE2ETest struct {
	module            *componentsv1alpha1.FeastOperator
	operatorNamespace string
	createdByTest     bool
	// rootT is the root test of the suite; fixture restores registered by
	// subtests use it so their failure awareness covers the whole suite.
	rootT *testing.T
}

func TestFeastOperator(t *testing.T) {
	// Kind mode fails preflight on missing inputs instead of skipping;
	// standalone mode keeps the existing core suite.
	kindMode := kindChecksEnabled()
	if kindMode {
		requireKindInputs(t)
		// The connected cluster must be the named dedicated Kind cluster
		// before any fixture operation runs: an ambient kubeconfig must
		// never receive Kind-mode singleton/ConfigMap lifecycle traffic.
		verifyKindClusterIdentity(t)
	}

	suite := &feastE2ETest{
		module: &componentsv1alpha1.FeastOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentsv1alpha1.FeastOperatorInstanceName,
			},
		},
		operatorNamespace: support.OperatorNamespace(),
		rootT:             t,
	}
	foundation := newFoundationTests(suite)

	err := k8sClient.Get(ctx, client.ObjectKeyFromObject(suite.module), suite.module)
	switch {
	case err == nil && kindMode:
		// The cluster-scoped singleton is only adopted on the verified
		// dedicated cluster with a matching ownership annotation.
		owner := suite.module.GetAnnotations()[fixtureOwnerAnnotation]
		if owner != kindClusterName() {
			t.Fatalf("FeastOperator %s exists with %s=%q (expected %q); refusing to adopt, mutate, or delete it",
				suite.module.Name, fixtureOwnerAnnotation, owner, kindClusterName())
		}
		if suite.module.GetDeletionTimestamp() != nil {
			// Suite-owned but terminating: wait boundedly for finalization
			// (never removing finalizers), then create a fresh marked
			// singleton below.
			t.Logf("suite-owned FeastOperator %s is terminating; waiting for finalization", suite.module.Name)
			waitForDeletedWithTimeout(t, suite.module, terminatingTimeout)
			// Build a fresh object: the terminating one carries
			// server-managed fields (UID, resourceVersion,
			// deletionTimestamp, finalizers) that must not be sent on
			// create.
			suite.module = &componentsv1alpha1.FeastOperator{
				ObjectMeta: metav1.ObjectMeta{
					Name:        componentsv1alpha1.FeastOperatorInstanceName,
					Annotations: map[string]string{fixtureOwnerAnnotation: kindClusterName()},
				},
			}
			suite.createdByTest = true
		} else {
			// Recover the known test state of a previous run; it remains
			// suite-owned and must execute teardown coverage.
			t.Logf("recovering suite-owned FeastOperator %s from a previous run (operator namespace %s)",
				suite.module.Name, suite.operatorNamespace)
			suite.createdByTest = true
		}
	case err == nil:
		t.Logf("using existing FeastOperator %s (operator namespace %s)", suite.module.Name, suite.operatorNamespace)
	case k8serr.IsNotFound(err):
		suite.createdByTest = true
	default:
		t.Fatalf("failed to get FeastOperator %s: %v", suite.module.Name, err)
	}

	if suite.createdByTest {
		if kindMode {
			// Mark the singleton as owned by this dedicated cluster's
			// suite before creating it.
			annotations := suite.module.GetAnnotations()
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[fixtureOwnerAnnotation] = kindClusterName()
			suite.module.SetAnnotations(annotations)
		}
		t.Cleanup(func() {
			// A failed Kind run preserves the suite-owned singleton (and
			// its operands) for inspection; recovery happens on the next
			// authorized run. Standalone behavior is unchanged.
			if kindMode && t.Failed() {
				t.Logf("preserving suite-owned FeastOperator %s after failed run", suite.module.Name)
				return
			}
			_ = k8sClient.Delete(ctx, suite.module)
		})
	}

	// Gate: if the operator is not running, fail immediately — don't
	// let subsequent tests hang waiting for resources that won't appear.
	eventuallyDeploymentReady(t, foundation.operatorDeploy)

	// Collect diagnostics before failure-aware cleanup decides what to
	// preserve. Registered after the singleton cleanup, so it runs first.
	if kindMode {
		foundation.registerKindDiagnostics(t)
	}

	// Create the module CR up front (when not already present) so that
	// subtests observing the deployed workload don't race its creation.
	if suite.createdByTest && suite.module.GetResourceVersion() == "" {
		suite.module.ResourceVersion = ""
		g := NewWithT(t)
		g.Expect(k8sClient.Create(ctx, suite.module)).To(Succeed())
	}

	t.Run("foundation", foundation.Execute)
}

func waitForDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	waitForDeletedWithTimeout(t, obj, timeout)
}

func waitForDeletedWithTimeout(t *testing.T, obj client.Object, waitTimeout time.Duration) {
	t.Helper()

	key := client.ObjectKeyFromObject(obj)
	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		fresh := obj.DeepCopyObject().(client.Object)
		err := k8sClient.Get(ctx, key, fresh)
		g.Expect(err).To(HaveOccurred(),
			"expected %T %s to be deleted, but it still exists (finalizers=%v)",
			obj, key, fresh.GetFinalizers())
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue(),
			"expected %T %s to be NotFound, got: %v", obj, key, err)
	}).WithContext(ctx).WithTimeout(waitTimeout).WithPolling(interval).Should(Succeed())
}

func waitForSingletonDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	waitForDeleted(t, obj)
	obj.SetResourceVersion("")
	obj.SetUID("")
}

func eventuallyDeploymentReady(t *testing.T, deploy *appsv1.Deployment) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(k.Get(deploy)).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(
		jq.Match(`.status.readyReplicas >= 1`),
	)
}

func waitForLabeledResourcesGone(t *testing.T, list client.ObjectList, opts ...client.ListOption) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		g.Expect(k8sClient.List(ctx, list, opts...)).To(Succeed())
		items, err := meta.ExtractList(list)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(items).To(BeEmpty())
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
}
