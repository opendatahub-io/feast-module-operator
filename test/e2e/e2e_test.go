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

	operatorConfigMapName = "opendatahub-feast-config"
	moduleCRDName         = "feastoperators.components.platform.opendatahub.io"

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
	k         *k8sm.Matcher

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

	k = k8sm.New(k8sClient, testScheme)

	return m.Run()
}

type feastE2ETest struct {
	module            *componentsv1alpha1.FeastOperator
	operatorNamespace string
	createdByTest     bool
}

func TestFeastOperator(t *testing.T) {
	suite := &feastE2ETest{
		module: &componentsv1alpha1.FeastOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentsv1alpha1.FeastOperatorInstanceName,
			},
		},
		operatorNamespace: support.OperatorNamespace(),
	}
	foundation := newFoundationTests(suite)

	err := k8sClient.Get(ctx, client.ObjectKeyFromObject(suite.module), suite.module)
	switch {
	case err == nil:
		t.Logf("using existing FeastOperator %s (operator namespace %s)", suite.module.Name, suite.operatorNamespace)
	case k8serr.IsNotFound(err):
		suite.createdByTest = true
		t.Cleanup(func() {
			_ = k8sClient.Delete(ctx, suite.module)
		})
	default:
		t.Fatalf("failed to get FeastOperator %s: %v", suite.module.Name, err)
	}

	// Gate: if the operator is not running, fail immediately — don't
	// let subsequent tests hang waiting for resources that won't appear.
	eventuallyDeploymentReady(t, foundation.operatorDeploy)

	t.Run("foundation", foundation.Execute)
}

func waitForDeleted(t *testing.T, obj client.Object) {
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
	}).WithContext(ctx).WithTimeout(timeout).WithPolling(interval).Should(Succeed())
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
