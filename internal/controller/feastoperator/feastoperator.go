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

	"k8s.io/client-go/tools/events"

	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
	"github.com/opendatahub-io/odh-platform-utilities/framework/render/kustomize/params"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
)

const (
	componentName = componentApi.FeastOperatorComponentName

	overlayODH   = "overlays/odh"
	overlayRhoai = "overlays/rhoai"

	// componentLabelKey is the ODH component label applied to rendered
	// resources and used by watch predicates, the capabilities ConfigMap,
	// and finalizer cleanup (app.opendatahub.io/<component>).
	componentLabelKey = "app.opendatahub.io/" + componentName

	// componentLabelValue is the value of the ODH component label.
	componentLabelValue = "true"

	// partOfLabelKey is the kubernetes recommended part-of label applied
	// at kustomize render time.
	partOfLabelKey = "app.kubernetes.io/part-of"
)

// imageParamMap matches the monolith's imageParamMap.
var imageParamMap = map[string]string{
	"RELATED_IMAGE_FEAST_OPERATOR":            "RELATED_IMAGE_ODH_FEAST_OPERATOR_IMAGE",
	"RELATED_IMAGE_FEATURE_SERVER":            "RELATED_IMAGE_ODH_FEATURE_SERVER_IMAGE",
	"RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE": "RELATED_IMAGE_ODH_KUBE_RBAC_PROXY_IMAGE",
}

// Module holds process-lifetime state for the feastoperator controller.
type Module struct {
	cfg          *moduleconfig.Config
	manifestInfo fwtypes.ManifestInfo
	// recorder reports Kubernetes events on the FeastOperator. Nil skips emission.
	recorder events.EventRecorder
}

// OverlayFor returns the manifests overlay selected for the given platform
// type: RHOAI platform types use the RHOAI overlay, everything else (ODH,
// unknown) falls back to the ODH overlay.
func OverlayFor(platformType moduleconfig.PlatformType) string {
	switch platformType {
	case moduleconfig.PlatformTypeSelfManagedRhoai:
		return overlayRhoai
	case moduleconfig.PlatformTypeManagedRhoai:
		return overlayRhoai
	default:
		return overlayODH
	}
}

// imageParamValues resolves the image parameter map from the environment.
// Empty values are skipped so that only non-empty RELATED_IMAGE_* variables
// are applied, mirroring the former odhdeploy.ApplyParams semantics.
func imageParamValues() map[string]string {
	values := params.FromEnv(imageParamMap)
	for key, value := range values {
		if value == "" {
			delete(values, key)
		}
	}

	return values
}

// NewModule creates a Module with one-shot computed state.
func NewModule(cfg *moduleconfig.Config) (*Module, error) {
	mi := fwtypes.ManifestInfo{
		Path:       cfg.ManifestsPath,
		ContextDir: componentName,
		SourcePath: OverlayFor(cfg.PlatformType),
	}

	// Apply image params once at startup (equivalent to Init in the monolith).
	if err := params.ApplyAtPath(mi.String(), "params.env", params.Replacement(imageParamValues())); err != nil {
		return nil, fmt.Errorf("failed to update images on path %s: %w", mi, err)
	}

	return &Module{
		cfg:          cfg,
		manifestInfo: mi,
	}, nil
}

// initialize appends the pre-resolved manifest info to the pipeline.
// Feast does not require namespace substitution in params.env.
func (m *Module) initialize(_ context.Context, rr *fwtypes.ReconciliationRequest) error {
	rr.Manifests = append(rr.Manifests, m.manifestInfo)
	return nil
}
