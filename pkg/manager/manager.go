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

package manager

import (
	"context"
	"fmt"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/spf13/viper"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8slabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	componentsv1alpha1 "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	"github.com/opendatahub-io/feast-module-operator/internal/controller/feastoperator"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	odhmanager "github.com/opendatahub-io/odh-platform-utilities/framework/manager"
	libcache "github.com/opendatahub-io/odh-platform-utilities/pkg/cache"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
)

const (
	healthCheckName = "healthz"
	readyCheckName  = "readyz"
)

type Option func(*ctrl.Options)

func NewScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(promv1.AddToScheme(scheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(scheme))

	return scheme
}

func New(
	ctx context.Context,
	kubeConfig *rest.Config,
	cfg *moduleconfig.Config,
	opts ...Option,
) (ctrl.Manager, error) {
	if kubeConfig == nil {
		return nil, fmt.Errorf("kubeconfig is nil")
	}
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	scheme := NewScheme()
	utilruntime.Must(configv1.AddToScheme(scheme))

	bootstrapConfig := rest.CopyConfig(kubeConfig)
	bootstrapConfig.Timeout = 10 * time.Second
	bootstrapClient, err := client.New(bootstrapConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("creating TLS profile bootstrap client: %w", err)
	}
	bootstrapCtx, cancelBootstrap := context.WithTimeout(ctx, 10*time.Second)
	tlsProfile, err := loadTLSProfile(bootstrapCtx, bootstrapClient)
	cancelBootstrap()
	if err != nil {
		return nil, fmt.Errorf("loading cluster TLS profile and adherence: %w", err)
	}
	tlsOpts, err := metricsTLSOptions(tlsProfile)
	if err != nil {
		return nil, fmt.Errorf("resolving metrics TLS options: %w", err)
	}

	viper.Set("rhai-applications-namespace", cfg.ApplicationsNamespace)
	cluster.SetRHAIApplicationNamespace(cfg.ApplicationsNamespace)

	// Namespace-scoped cache config: scope informers to the applications
	// namespace so the operator only watches its own resources.
	nsCache := map[string]cache.Config{
		cfg.ApplicationsNamespace: {},
	}

	mgrOpts := ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   cfg.Controller.Metrics.BindAddress,
			SecureServing: true,
			TLSOpts:       tlsOpts,
			CertDir:       cfg.Controller.Metrics.CertDir,
		},
		HealthProbeBindAddress:        cfg.Controller.Health.BindAddress,
		PprofBindAddress:              cfg.Controller.Pprof.BindAddress,
		LeaderElection:                cfg.Controller.LeaderElection.Enabled,
		LeaderElectionID:              cfg.Controller.LeaderElection.ID,
		LeaderElectionReleaseOnCancel: true,
		Cache: cache.Options{
			DefaultTransform: libcache.StripUnusedFields(),
			ByObject: map[client.Object]cache.ByObject{
				&componentsv1alpha1.FeastOperator{}:         {Label: k8slabels.Everything()},
				&apiextensionsv1.CustomResourceDefinition{}: {Label: k8slabels.Everything()},
				&appsv1.Deployment{}:                        {Namespaces: nsCache},
				&corev1.Service{}:                           {Namespaces: nsCache},
				&corev1.ServiceAccount{}:                    {Namespaces: nsCache},
				&rbacv1.Role{}:                              {Namespaces: nsCache},
				&rbacv1.RoleBinding{}:                       {Namespaces: nsCache},
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
	}

	for _, opt := range opts {
		if opt != nil {
			opt(&mgrOpts)
		}
	}
	mgrOpts.Metrics.SecureServing = true
	mgrOpts.Metrics.TLSOpts = tlsOpts
	mgrOpts.Metrics.CertDir = cfg.Controller.Metrics.CertDir

	ctrlMgr, err := ctrl.NewManager(kubeConfig, mgrOpts)
	if err != nil {
		return nil, fmt.Errorf("creating manager: %w", err)
	}

	mgr := odhmanager.New(
		ctrlMgr,
		odhmanager.WithManifestsBasePath(cfg.ManifestsPath),
	)

	if err := feastoperator.NewReconciler(ctx, mgr, cfg, cfg.Release()); err != nil {
		return nil, fmt.Errorf("creating feastoperator reconciler: %w", err)
	}

	if err := mgr.AddHealthzCheck(healthCheckName, healthz.Ping); err != nil {
		return nil, fmt.Errorf("setting up health check: %w", err)
	}
	if err := mgr.AddReadyzCheck(readyCheckName, healthz.Ping); err != nil {
		return nil, fmt.Errorf("setting up ready check: %w", err)
	}

	if err := registerTLSProfileWatcher(ctrlMgr, tlsProfile); err != nil {
		return nil, err
	}

	return mgr, nil
}
