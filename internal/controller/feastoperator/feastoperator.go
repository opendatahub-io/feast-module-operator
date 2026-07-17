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

	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
	odhdeploy "github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
)

const (
	componentName = componentApi.FeastOperatorComponentName

	overlayODH   = "overlays/odh"
	overlayRhoai = "overlays/rhoai"
)

// imageParamMap matches the monolith's imageParamMap.
var imageParamMap = map[string]string{
	"RELATED_IMAGE_FEAST_OPERATOR": "RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE",
	"RELATED_IMAGE_FEATURE_SERVER": "RELATED_IMAGE_ODH_FEATURE_SERVER_IMAGE",
}

// Module holds process-lifetime state for the feastoperator controller.
type Module struct {
	cfg          *moduleconfig.Config
	manifestInfo odhtypes.ManifestInfo
}

// NewModule creates a Module with one-shot computed state.
func NewModule(cfg *moduleconfig.Config) (*Module, error) {
	// Select overlay based on platform.
	overlay := overlayODH
	platform := common.Platform(cfg.PlatformName)
	if platform == cluster.SelfManagedRhoai || platform == cluster.ManagedRhoai {
		overlay = overlayRhoai
	}

	mi := odhtypes.ManifestInfo{
		Path:       cfg.ManifestsPath,
		ContextDir: componentName,
		SourcePath: overlay,
	}

	// Apply image params once at startup (equivalent to Init in the monolith).
	if err := odhdeploy.ApplyParams(mi.String(), "params.env", imageParamMap); err != nil {
		return nil, fmt.Errorf("failed to update images on path %s: %w", mi, err)
	}

	return &Module{
		cfg:          cfg,
		manifestInfo: mi,
	}, nil
}

// initialize appends the pre-resolved manifest info to the pipeline.
// Feast does not require namespace substitution in params.env.
func (m *Module) initialize(_ context.Context, rr *odhtypes.ReconciliationRequest) error {
	rr.Manifests = append(rr.Manifests, m.manifestInfo)
	return nil
}
