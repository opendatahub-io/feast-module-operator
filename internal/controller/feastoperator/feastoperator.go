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
	"os"
	"path"
	"path/filepath"

	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	fwtypes "github.com/opendatahub-io/odh-platform-utilities/framework/controller/types"
	kfs "github.com/opendatahub-io/odh-platform-utilities/framework/render/kustomize/fs"
	"github.com/opendatahub-io/odh-platform-utilities/framework/render/kustomize/params"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
)

const (
	componentName = componentApi.FeastOperatorComponentName

	overlayODH   = "overlays/odh"
	overlayRhoai = "overlays/rhoai"

	// paramsEnvFile is the kustomize env-style parameter file written into
	// the module filesystem at startup (image replacements) and per
	// reconcile (runtime OIDC values).
	paramsEnvFile = "params.env"

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
	// kustomizeFS is the process-lifetime copy-on-write filesystem over the
	// image-baked manifests. Its read-only base is rooted at the physical
	// manifests directory; every parameter write lands in a per-Module
	// in-memory overlay, and the render action reads through this same
	// instance so it observes those writes.
	kustomizeFS filesys.FileSystem
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
	kustomizeFS, err := newManifestsFS(cfg.ManifestsPath)
	if err != nil {
		return nil, err
	}

	mi := fwtypes.ManifestInfo{
		// Path is intentionally empty: the manifest descriptor is relative to
		// the module filesystem root, which is already rooted at the physical
		// manifests directory.
		Path:       "",
		ContextDir: componentName,
		SourcePath: OverlayFor(cfg.PlatformType),
	}

	// Apply image params once at startup (equivalent to Init in the monolith).
	if err := params.Apply(kustomizeFS, manifestParamsEnvPath(mi), params.Replacement(imageParamValues())); err != nil {
		return nil, fmt.Errorf("failed to update images on path %s: %w", mi, err)
	}

	return &Module{
		cfg:          cfg,
		manifestInfo: mi,
		kustomizeFS:  kustomizeFS,
	}, nil
}

// initialize appends the pre-resolved manifest info to the pipeline.
// Feast does not require namespace substitution in params.env.
func (m *Module) initialize(_ context.Context, rr *fwtypes.ReconciliationRequest) error {
	rr.Manifests = append(rr.Manifests, m.manifestInfo)
	return nil
}

// manifestParamsEnvPath returns the params.env path of a manifest descriptor
// relative to the module filesystem root. The descriptor components are
// joined directly: ManifestInfo.String must not be used as a filesystem path
// because it can append a namespace suffix.
func manifestParamsEnvPath(mi fwtypes.ManifestInfo) string {
	return path.Join(mi.Path, mi.ContextDir, mi.SourcePath, paramsEnvFile)
}

// newManifestsFS builds the copy-on-write filesystem shared by parameter
// application and rendering. The physical manifests directory is wrapped
// read-only and NewUnionFs layers a per-Module in-memory overlay above it:
// reads prefer the overlay and writes only ever reach the overlay, so the
// image-baked manifests are never mutated at runtime.
func newManifestsFS(manifestsPath string) (filesys.FileSystem, error) {
	root, err := absoluteManifestsRoot(manifestsPath)
	if err != nil {
		return nil, err
	}

	base, err := newReadOnlyManifestsBase(root)
	if err != nil {
		return nil, err
	}

	union, err := kfs.NewUnionFs(base)
	if err != nil {
		return nil, fmt.Errorf("creating union filesystem for manifests root %s: %w", root, err)
	}

	return union, nil
}

// newReadOnlyManifestsBase roots the on-disk filesystem at the given absolute
// directory and wraps it read-only. The read-only protection is explicit in
// the construction so it does not depend on filesystem permissions, which
// can be bypassed (e.g. when running as root).
func newReadOnlyManifestsBase(root string) (filesys.FileSystem, error) {
	base, err := kfs.NewBasePathFs(kfs.NewFsOnDisk(), root)
	if err != nil {
		return nil, fmt.Errorf("creating base path filesystem for manifests root %s: %w", root, err)
	}

	return kfs.NewReadOnlyFs(base), nil
}

// absoluteManifestsRoot resolves the configured manifests path to a cleaned
// absolute directory; empty and "." mean the working directory. The
// normalization is required: the base path filesystem rejects child paths of
// a relative "." base, and params.Apply then masks the failed existence
// check as a no-op.
func absoluteManifestsRoot(manifestsPath string) (string, error) {
	if manifestsPath == "" || manifestsPath == "." {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolving manifests path %q to the working directory: %w", manifestsPath, err)
		}

		return filepath.Clean(wd), nil
	}

	abs, err := filepath.Abs(filepath.Clean(manifestsPath))
	if err != nil {
		return "", fmt.Errorf("resolving manifests path %q to an absolute path: %w", manifestsPath, err)
	}

	return abs, nil
}
