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

	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

const (
	// platformConfigCMName is the ConfigMap created by the ODH platform operator
	// containing the platformVersion key. Format: odh-<modulename>-config.
	platformConfigCMName = "odh-feastoperator-config"

	// platformVersionKey is the data key in the platform config ConfigMap.
	platformVersionKey = "platformVersion"

	// platformReleaseName is the name used in status.releases to track the
	// platform version the module has reconciled against.
	platformReleaseName = "platform"
)

// reconcilePlatformVersion implements the platform version handshake protocol.
// It reads the platform version from the odh-feastoperator-config ConfigMap,
// compares it with the version recorded in status.releases[name="platform"],
// performs any upgrade work if they differ, and writes the new version to status.
func (m *Module) reconcilePlatformVersion(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	log := logf.FromContext(ctx)

	obj, ok := rr.Instance.(*componentApi.FeastOperator)
	if !ok {
		return fmt.Errorf("instance is not a FeastOperator")
	}

	platformVersion, err := m.getPlatformVersion(ctx, rr.Client)
	if err != nil {
		if k8serr.IsNotFound(err) {
			// ConfigMap not yet created by the platform — skip handshake.
			// This happens during initial deployment before the platform reconciles.
			log.V(1).Info("Platform config ConfigMap not found, skipping version handshake",
				"configmap", platformConfigCMName)
			return nil
		}
		return fmt.Errorf("failed to read platform version: %w", err)
	}

	if platformVersion == "" {
		log.V(1).Info("Platform version is empty in ConfigMap, skipping handshake")
		return nil
	}

	currentVersion := getPlatformRelease(obj)

	if currentVersion == platformVersion {
		return nil
	}

	log.Info("Platform version changed, performing upgrade handshake",
		"previousVersion", currentVersion,
		"newVersion", platformVersion,
	)

	if err := m.handlePlatformUpgrade(ctx, rr, currentVersion, platformVersion); err != nil {
		return err
	}

	setPlatformRelease(obj, platformVersion)

	log.Info("Platform version handshake complete", "version", platformVersion)
	return nil
}

// getPlatformVersion reads the platformVersion from the odh-feastoperator-config ConfigMap.
func (m *Module) getPlatformVersion(ctx context.Context, cl client.Client) (string, error) {
	cm := &corev1.ConfigMap{}
	err := cl.Get(ctx, client.ObjectKey{
		Name:      platformConfigCMName,
		Namespace: m.cfg.ApplicationsNamespace,
	}, cm)
	if err != nil {
		return "", err
	}

	return cm.Data[platformVersionKey], nil
}

// handlePlatformUpgrade performs version-specific upgrade logic when the platform
// version advances. Add migration steps here as needed for future versions.
func (m *Module) handlePlatformUpgrade(_ context.Context, _ *odhtypes.ReconciliationRequest, _, _ string) error {
	// No version-specific migrations needed for the initial release.
	// Future migrations can be gated on oldVersion/newVersion comparisons here.
	return nil
}

func getPlatformRelease(obj *componentApi.FeastOperator) string {
	for _, r := range obj.Status.Releases {
		if r.Name == platformReleaseName {
			return r.Version
		}
	}
	return ""
}

func setPlatformRelease(obj *componentApi.FeastOperator, version string) {
	for i, r := range obj.Status.Releases {
		if r.Name == platformReleaseName {
			obj.Status.Releases[i].Version = version
			return
		}
	}
	obj.Status.Releases = append(obj.Status.Releases, common.ComponentRelease{
		Name:    platformReleaseName,
		Version: version,
	})
}
