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
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
)

const (
	dataRegistryNamespaceName     = "rhoai-data-registry"
	dataRegistryEnabledLabelKey   = "dataregistry.opendatahub.io/enabled"
	dataRegistryEnabledLabelValue = "true"
)

// reconcileDataRegistryNamespace provisions the dedicated Data Registry namespace
// when the capability is enabled.
//
// The namespace is always rhoai-data-registry (hardcoded). Custom namespace
// selection is deferred.
func (m *Module) reconcileDataRegistryNamespace(ctx context.Context, rr *fwtypes.ReconciliationRequest) error {
	feast, ok := rr.Instance.(*componentApi.FeastOperator)
	if !ok {
		return errors.New("instance is not a FeastOperator")
	}

	_, drEnabled := m.resolveCapabilities(feast)
	if !drEnabled {
		return m.removeDataRegistryLabel(ctx, rr)
	}

	log := logf.FromContext(ctx)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: dataRegistryNamespaceName,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, rr.Client, ns, func() error {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[dataRegistryEnabledLabelKey] = dataRegistryEnabledLabelValue
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to reconcile Data Registry namespace %s: %w", dataRegistryNamespaceName, err)
	}

	log.V(1).Info("Reconciled Data Registry namespace",
		"namespace", dataRegistryNamespaceName,
		"operation", op,
	)

	return nil
}

// removeDataRegistryLabel removes the dataregistry.opendatahub.io/enabled label
// from the Data Registry namespace when the capability is disabled. The namespace
// itself is preserved to avoid deleting user data.
func (m *Module) removeDataRegistryLabel(ctx context.Context, rr *fwtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)

	ns := &corev1.Namespace{}
	if err := rr.Client.Get(ctx, client.ObjectKey{Name: dataRegistryNamespaceName}, ns); err != nil {
		if k8serr.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Data Registry namespace %s: %w", dataRegistryNamespaceName, err)
	}

	if ns.Labels[dataRegistryEnabledLabelKey] != dataRegistryEnabledLabelValue {
		return nil
	}

	patch := client.MergeFrom(ns.DeepCopy())
	delete(ns.Labels, dataRegistryEnabledLabelKey)
	if err := rr.Client.Patch(ctx, ns, patch); err != nil {
		return fmt.Errorf("failed to remove enabled label from Data Registry namespace %s: %w", dataRegistryNamespaceName, err)
	}

	log.Info("Removed enabled label from Data Registry namespace",
		"namespace", dataRegistryNamespaceName,
	)

	return nil
}
