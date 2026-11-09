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

package operator

import (
	"testing"

	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
)

// The operand overlays (config/manifests/feastoperator default and rhoai)
// pass these flags to the upstream Feast binary. The module operator's own
// deployment currently uses environment variables, but defining the same
// flags keeps the command configurable and compatible if flags are wired in.
// Every one must be defined: cobra exits on the first unknown flag, so an
// undefined flag crash-loops the operator (RHOAIENG-100779, RHAI-10170).
func TestOperatorCommandDefinesOverlayFlags(t *testing.T) {
	cmd := NewCommand()
	for _, name := range []string{
		"metrics-bind-address",
		"metrics-cert-path",
		"health-probe-bind-address",
		"leader-elect",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("operator command does not define --%s, which the operand overlays use", name)
		}
	}
}

func TestApplyFlagOverridesLeaderElect(t *testing.T) {
	newConfig := func() *moduleconfig.Config {
		cfg := &moduleconfig.Config{}
		cfg.Controller.LeaderElection.Enabled = true
		return cfg
	}

	t.Run("explicit false disables the config default", func(t *testing.T) {
		cmd := NewCommand()
		if err := cmd.Flags().Set("leader-elect", "false"); err != nil {
			t.Fatal(err)
		}
		cfg := newConfig()
		applyFlagOverrides(cmd, cfg, "", "", "", false)
		if cfg.Controller.LeaderElection.Enabled {
			t.Error("--leader-elect=false must disable leader election")
		}
	})

	t.Run("explicit true enables", func(t *testing.T) {
		cmd := NewCommand()
		if err := cmd.Flags().Set("leader-elect", "true"); err != nil {
			t.Fatal(err)
		}
		cfg := newConfig()
		cfg.Controller.LeaderElection.Enabled = false
		applyFlagOverrides(cmd, cfg, "", "", "", true)
		if !cfg.Controller.LeaderElection.Enabled {
			t.Error("--leader-elect=true must enable leader election")
		}
	})

	t.Run("omitted flag preserves the config setting", func(t *testing.T) {
		cmd := NewCommand()
		cfg := newConfig()
		applyFlagOverrides(cmd, cfg, "", "", "", false)
		if !cfg.Controller.LeaderElection.Enabled {
			t.Error("an omitted --leader-elect must preserve the loaded configuration")
		}
	})
}
