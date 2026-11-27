package feastoperator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func TestMigrateDeploymentSelector(t *testing.T) {
	const namespace = "applications"
	failure := errors.New("client failed")
	for _, tc := range []struct {
		name           string
		selector       *metav1.LabelSelector
		absent         bool
		getError       bool
		deleteError    bool
		deleteRace     bool
		wantDeleteCall bool
	}{
		{name: "fresh install", absent: true},
		{name: "nil selector is not migrated"},
		{name: "current selector preserves operand", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "feast-operator"}}},
		{name: "current selector with extra labels preserves operand", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "feast-operator", "control-plane": "controller-manager"}}},
		{name: "legacy selector is removed for recreation", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"control-plane": "controller-manager"}}, wantDeleteCall: true},
		{name: "wrong selector value is removed", selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "old-feast-operator"}}, wantDeleteCall: true},
		{name: "get error is returned", getError: true},
		{name: "delete error is returned", selector: &metav1.LabelSelector{}, deleteError: true, wantDeleteCall: true},
		{name: "concurrent deletion is tolerated", selector: &metav1.LabelSelector{}, deleteRace: true, wantDeleteCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := client.ObjectKey{Namespace: namespace, Name: deploymentName}
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, UID: "existing-operand"}, Spec: appsv1.DeploymentSpec{Selector: tc.selector}}
			otherNamespace := deployment.DeepCopy()
			otherNamespace.Namespace = "unrelated"
			otherName := deployment.DeepCopy()
			otherName.Name = "unrelated"
			objects := []client.Object{otherNamespace, otherName}
			if !tc.absent {
				objects = append(objects, deployment)
			}
			var deleted []client.ObjectKey
			injectErrors := true
			cli := fake.NewClientBuilder().WithScheme(initCapabilitiesTestScheme()).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, cli client.WithWatch, requested client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if injectErrors && tc.getError && requested == key {
						return failure
					}
					return cli.Get(ctx, requested, obj, opts...)
				},
				Delete: func(ctx context.Context, cli client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					deleted = append(deleted, client.ObjectKeyFromObject(obj))
					if tc.deleteError {
						return failure
					}
					if err := cli.Delete(ctx, obj, opts...); err != nil {
						return err
					}
					if tc.deleteRace {
						return k8serr.NewNotFound(schema.GroupResource{Group: "apps", Resource: "deployments"}, obj.GetName())
					}
					return nil
				},
			}).Build()
			module := &Module{cfg: &moduleconfig.Config{ApplicationsNamespace: namespace}}
			err := module.migrateDeploymentSelector(t.Context(), &odhtypes.ReconciliationRequest{Client: cli})
			if tc.getError || tc.deleteError {
				require.ErrorIs(t, err, failure)
			} else {
				require.NoError(t, err)
			}
			injectErrors = false
			if tc.wantDeleteCall {
				require.Equal(t, []client.ObjectKey{key}, deleted)
			} else {
				require.Empty(t, deleted)
			}
			fresh := &appsv1.Deployment{}
			err = cli.Get(t.Context(), key, fresh)
			if tc.absent || (tc.wantDeleteCall && !tc.deleteError) {
				require.True(t, k8serr.IsNotFound(err))
			} else {
				require.NoError(t, err)
				require.Equal(t, deployment.UID, fresh.UID)
				require.Equal(t, deployment.Spec, fresh.Spec)
			}
			for _, unrelated := range []*appsv1.Deployment{otherNamespace, otherName} {
				require.NoError(t, cli.Get(t.Context(), client.ObjectKeyFromObject(unrelated), fresh))
				require.Equal(t, unrelated.UID, fresh.UID)
				require.Equal(t, unrelated.Spec, fresh.Spec)
			}
		})
	}
}
