//go:build integration

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

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/lburgazzoli/gomega-matchers/pkg/matchers/jq"
	k8sm "github.com/lburgazzoli/gomega-matchers/pkg/matchers/k8s"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	feastoperatorcontroller "github.com/opendatahub-io/feast-module-operator/internal/controller/feastoperator"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/feast-module-operator/test/support"
	fwmanager "github.com/opendatahub-io/odh-platform-utilities/framework/manager"
)

const (
	timeout  = 90 * time.Second
	interval = 2 * time.Second

	labelPartOf            = "platform.opendatahub.io/part-of"
	annotationInstanceName = "platform.opendatahub.io/instance.name"
	annotationInstanceUID  = "platform.opendatahub.io/instance.uid"
	annotationType         = "platform.opendatahub.io/type"
	annotationVersion      = "platform.opendatahub.io/version"

	// testPlatformVersion is the platform version the test publishes in the
	// platform config ConfigMap, emulating the ODH platform operator.
	testPlatformVersion = "2.22.0"
	moduleCRDName       = "feastoperators.components.platform.opendatahub.io"
)

var (
	ctx                    context.Context
	cancel                 context.CancelFunc
	k8sClient              client.Client
	k                      *k8sm.Operations
	operatorReleaseVersion string
	operatorPlatformType   string
	testScheme             = runtime.NewScheme()
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

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		return 1
	}

	directClient, err := client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client: %v\n", err)
		return 1
	}

	testNamespace := support.IntegrationTestNamespace()

	if err := support.EnsureNamespace(ctx, directClient, testNamespace); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create namespace: %v\n", err)
		return 1
	}

	moduleCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
	}
	if err := directClient.Get(ctx, client.ObjectKeyFromObject(moduleCRD), moduleCRD); err != nil {
		fmt.Fprintf(os.Stderr, "Expected CRD %s to be installed before running integration tests: %v\n", moduleCRDName, err)
		return 1
	}

	// Clean up leftovers from previous runs.
	_ = directClient.DeleteAllOf(ctx, &componentsv1alpha1.FeastOperator{})
	_ = directClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(testNamespace))
	_ = directClient.DeleteAllOf(ctx, &corev1.Service{}, client.InNamespace(testNamespace))

	// Emulate the manager Deployment: the operator configuration is driven
	// by ODH_MODULE_OPERATOR_* env vars. The platform type is inherited from
	// the environment when injected (ODH_MODULE_OPERATOR_PLATFORM_TYPE);
	// the remaining env vars mirror those set on the manager container.
	// Loading through moduleconfig.Load exercises the same env binding the
	// real operator uses, so an injected platform type genuinely drives
	// manifest selection.
	_ = os.Setenv("ODH_MODULE_OPERATOR_MANIFESTS_PATH", support.MustProjectFile("config", "manifests"))
	_ = os.Setenv("ODH_MODULE_OPERATOR_APPLICATIONS_NAMESPACE", testNamespace)

	// Emulate the ODH platform operator: it publishes the platform version
	// in a ConfigMap in the applications namespace; the operator records it
	// in status.releases[name="platform"] through the version handshake.
	platformCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      feastoperatorcontroller.PlatformConfigCMName,
			Namespace: testNamespace,
		},
		Data: map[string]string{
			feastoperatorcontroller.PlatformVersionKey: testPlatformVersion,
		},
	}
	if err := directClient.Create(ctx, platformCM); err != nil && !k8serr.IsAlreadyExists(err) {
		fmt.Fprintf(os.Stderr, "Failed to create platform config ConfigMap: %v\n", err)
		return 1
	}

	// Expected platform type: the value a deployment would inject. Kept
	// separate from moduleCfg so the assertions validate that Load() picked
	// up the injected value rather than comparing the config against itself.
	operatorPlatformType = os.Getenv("ODH_MODULE_OPERATOR_PLATFORM_TYPE")
	if operatorPlatformType == "" {
		operatorPlatformType = string(moduleconfig.DefaultPlatformType)
	}

	moduleCfg, err := moduleconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load module config: %v\n", err)
		return 1
	}
	operatorReleaseVersion = moduleCfg.Release().Version.String()

	ctrlMgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         testScheme,
		Metrics:        metricsserver.Options{BindAddress: "0"},
		LeaderElection: false,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				testNamespace:       {},
				cache.AllNamespaces: {},
			},
		},
		Client: client.Options{
			Cache: &client.CacheOptions{
				Unstructured: true,
				DisableFor: []client.Object{
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
			},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create manager: %v\n", err)
		return 1
	}

	mgr := fwmanager.New(ctrlMgr, fwmanager.WithManifestsBasePath(
		support.MustProjectFile("config", "manifests")))

	if err := feastoperatorcontroller.NewReconciler(ctx, mgr, moduleCfg, moduleCfg.Release()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create reconciler: %v\n", err)
		return 1
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Manager exited with error: %v\n", err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		fmt.Fprintf(os.Stderr, "Failed to sync manager cache\n")
		return 1
	}

	k8sClient = mgr.GetClient()
	k = k8sm.Using(k8sClient)

	_ = directClient.Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: ctrl.ObjectMeta{Name: "integration-test-role"},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     []string{"*"},
		}},
	})
	_ = directClient.Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: ctrl.ObjectMeta{Name: "integration-test-binding"},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "integration-test-role",
		},
		Subjects: []rbacv1.Subject{{
			Kind:     "Group",
			Name:     "system:masters",
			APIGroup: "rbac.authorization.k8s.io",
		}},
	})

	return m.Run()
}

type feastTest struct {
	module         *componentsv1alpha1.FeastOperator
	moduleCRD      *apiextensionsv1.CustomResourceDefinition
	workloadDeploy *appsv1.Deployment
}

func TestFeastOperator(t *testing.T) {
	testNamespace := support.IntegrationTestNamespace()

	suite := &feastTest{
		module: &componentsv1alpha1.FeastOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: componentsv1alpha1.FeastOperatorInstanceName,
			},
		},
		moduleCRD: &apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: moduleCRDName},
		},
		workloadDeploy: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "feast-operator-controller-manager",
				Namespace: testNamespace,
			},
		},
	}
	foundation := &foundationTests{feastTest: suite}

	// Delete any leftover CR from a previous run before starting.
	_ = k8sClient.Delete(ctx, suite.module)
	waitForSingletonDeleted(t, suite.module)

	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, suite.module)
	})

	t.Run("foundation", foundation.Execute)
	t.Run("capabilities", (&capabilityTests{feastTest: suite}).Execute)
}

func waitForDeleted(t *testing.T, obj client.Object) {
	t.Helper()

	g := NewWithT(t)
	g.Eventually(func(g Gomega) {
		fresh := obj.DeepCopyObject().(client.Object)
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), fresh)
		g.Expect(k8serr.IsNotFound(err)).To(BeTrue())
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
