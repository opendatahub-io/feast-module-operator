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

	"github.com/blang/semver/v4"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func (m *Module) upgradeIfNeeded(ctx context.Context, rr *odhtypes.ReconciliationRequest) error {
	obj, ok := rr.Instance.(*componentApi.FeastOperator)
	if !ok {
		return fmt.Errorf("instance is not a FeastOperator")
	}

	prevVersion := getPlatformRelease(obj)
	if prevVersion == "" || prevVersion == "0.0.0" {
		return nil
	}

	prev, err := semver.Parse(prevVersion)
	if err != nil {
		return fmt.Errorf("failed to parse previous platform version %q: %w", prevVersion, err)
	}

	if !rr.Release.Version.GT(prev) {
		return nil
	}

	return m.upgrade(ctx, prevVersion, rr)
}

// upgrade runs idempotent migrations when the platform version advances.
// Add version-gated migrations here as needed.
func (m *Module) upgrade(_ context.Context, prevVersion string, rr *odhtypes.ReconciliationRequest) error {
	_ = prevVersion
	_ = rr
	return nil
}
