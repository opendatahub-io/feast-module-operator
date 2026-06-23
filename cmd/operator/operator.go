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
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Start the module operator",
		RunE:  run,
	}

	return cmd
}

func run(cmd *cobra.Command, _ []string) error {
	cfg, err := moduleconfig.Load()
	if err != nil {
		return fmt.Errorf("loading operator config: %w", err)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(false)))

	mgr, err := modulemgr.New(cmd.Context(), ctrl.GetConfigOrDie(), cfg)
	if err != nil {
		return err
	}

	return mgr.Start(cmd.Context())
}
