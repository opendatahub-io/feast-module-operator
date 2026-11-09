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
	"fmt"

	"github.com/spf13/cobra"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	modulemgr "github.com/opendatahub-io/feast-module-operator/pkg/manager"
)

func NewCommand() *cobra.Command {
	var (
		metricsBindAddress     string
		metricsCertPath        string
		healthProbeBindAddress string
		leaderElect            bool
	)
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Start the module operator",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, metricsBindAddress, metricsCertPath, healthProbeBindAddress, leaderElect)
		},
	}
	// The shipped overlays (config/manifests/feastoperator default and rhoai)
	// pass these flags; every one of them must be defined or cobra exits on
	// the first unknown flag and the operator crash-loops (the defect shape
	// from RHOAIENG-100779 and RHAI-10170).
	cmd.Flags().StringVar(&metricsBindAddress, "metrics-bind-address", "",
		"The bind address for the metrics server.")
	cmd.Flags().StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	cmd.Flags().StringVar(&healthProbeBindAddress, "health-probe-bind-address", "",
		"The bind address for the health probe server.")
	cmd.Flags().BoolVar(&leaderElect, "leader-elect", false,
		"Enable leader election for the operator.")

	return cmd
}

func run(
	cmd *cobra.Command, metricsBindAddress, metricsCertPath, healthProbeBindAddress string, leaderElect bool,
) error {
	cfg, err := moduleconfig.Load()
	if err != nil {
		return fmt.Errorf("loading operator config: %w", err)
	}
	applyFlagOverrides(cmd, cfg, metricsBindAddress, metricsCertPath, healthProbeBindAddress, leaderElect)

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	mgr, err := modulemgr.New(cmd.Context(), ctrl.GetConfigOrDie(), cfg)
	if err != nil {
		return err
	}

	return mgr.Start(cmd.Context())
}

// applyFlagOverrides applies command-line flags over the loaded configuration.
// String flags override when non-empty; --leader-elect applies its explicit
// value only when supplied, so --leader-elect=false disables the config
// default and an omitted flag preserves it.
func applyFlagOverrides(
	cmd *cobra.Command, cfg *moduleconfig.Config,
	metricsBindAddress, metricsCertPath, healthProbeBindAddress string, leaderElect bool,
) {
	if metricsBindAddress != "" {
		cfg.Controller.Metrics.BindAddress = metricsBindAddress
	}
	if metricsCertPath != "" {
		cfg.Controller.Metrics.CertDir = metricsCertPath
	}
	if healthProbeBindAddress != "" {
		cfg.Controller.Health.BindAddress = healthProbeBindAddress
	}
	if cmd.Flags().Changed("leader-elect") {
		cfg.Controller.LeaderElection.Enabled = leaderElect
	}
}
