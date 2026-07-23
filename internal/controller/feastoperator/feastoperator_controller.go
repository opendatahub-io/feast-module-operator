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
	"fmt"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	extv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/deploy"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/gc"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/render/kustomize"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/status/deployments"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/actions/status/releases"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/handlers"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/predicates"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/predicates/component"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/predicates/resources"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/reconciler"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/metadata/labels"
)

// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=feastoperators,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=feastoperators/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=components.platform.opendatahub.io,resources=feastoperators/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;serviceaccounts;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs;cronjobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;delete;patch;update
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;delete;update
// +kubebuilder:rbac:groups="",resources=secrets;namespaces,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups="",resources=pods;pods/exec,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete;update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;delete;patch;update
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;delete;patch;update
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=feast.dev,resources=featurestores;featurestores/status;featurestores/finalizers,verbs=get;list;watch;create;delete;patch;update
// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=authorization.k8s.io,resources=subjectaccessreviews,verbs=create
// +kubebuilder:rbac:groups=kubeflow.org,resources=notebooks,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=subjectaccessreviews,verbs=get;list;watch;create;update;delete
// +kubebuilder:rbac:urls=/metrics,verbs=get

func NewReconciler(
	ctx context.Context,
	mgr ctrl.Manager,
	cfg *moduleconfig.Config,
	rel common.Release,
) error {
	m, err := NewModule(cfg)
	if err != nil {
		return err
	}

	r, err := reconciler.ReconcilerFor(mgr, &componentApi.FeastOperator{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.ClusterRoleBinding{}).
		Owns(&rbacv1.ClusterRole{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}, reconciler.WithPredicates(predicates.DefaultDeploymentPredicate)).
		Owns(&batchv1.Job{}).
		Owns(&promv1.ServiceMonitor{}).
		Watches(
			&extv1.CustomResourceDefinition{},
			reconciler.WithEventHandler(
				handlers.ToNamed(componentApi.FeastOperatorInstanceName)),
			reconciler.WithPredicates(
				component.ForLabel(labels.ODH.Component(componentName), labels.True)),
		).
		Watches(
			&corev1.ConfigMap{},
			reconciler.WithEventHandler(
				handlers.ToNamed(componentApi.FeastOperatorInstanceName)),
			reconciler.WithPredicates(
				resources.CreatedOrUpdatedOrDeletedNamed(platformConfigCMName)),
		).
		WithAction(m.initialize).
		WithAction(m.upgradeIfNeeded).
		WithAction(m.setKustomizedParams).
		WithAction(releases.NewAction()).
		WithAction(m.reconcilePlatformVersion).
		WithAction(kustomize.NewAction(
			kustomize.WithLabel(labels.ODH.Component(componentName), labels.True),
			kustomize.WithLabel(labels.K8SCommon.PartOf, componentName),
		)).
		WithAction(m.migrateDeploymentSelector).
		WithAction(deploy.NewAction(
			deploy.WithCache(),
			deploy.WithApplyOrder(),
		)).
		WithAction(deployments.NewAction()).
		WithAction(gc.NewAction(
			gc.InNamespace(cfg.ApplicationsNamespace),
		)).
		WithFinalizer(m.cleanupClusterResources).
		WithConditions(
			"DeploymentsAvailable",
		).
		Build(ctx)

	if err != nil {
		return err
	}

	r.Release = rel

	return nil
}

// cleanupClusterResources removes cluster-scoped resources (ClusterRoles, ClusterRoleBindings)
// that cannot use ownerReferences for garbage collection.
func (m *Module) cleanupClusterResources(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)
	listOpts := []client.ListOption{
		client.MatchingLabels{
			labels.ODH.Component(componentName): labels.True,
		},
	}

	log.Info("Cleaning up cluster-scoped resources for FeastOperator")

	crbList := &rbacv1.ClusterRoleBindingList{}
	if err := rr.Client.List(ctx, crbList, listOpts...); err != nil {
		return fmt.Errorf("failed to list ClusterRoleBindings: %w", err)
	}
	for i := range crbList.Items {
		if err := rr.Client.Delete(ctx, &crbList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete ClusterRoleBinding %s: %w", crbList.Items[i].Name, err)
		}
	}

	crList := &rbacv1.ClusterRoleList{}
	if err := rr.Client.List(ctx, crList, listOpts...); err != nil {
		return fmt.Errorf("failed to list ClusterRoles: %w", err)
	}
	for i := range crList.Items {
		if err := rr.Client.Delete(ctx, &crList.Items[i]); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to delete ClusterRole %s: %w", crList.Items[i].Name, err)
		}
	}

	return nil
}
