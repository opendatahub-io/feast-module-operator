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

package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/blang/semver/v4"
	"github.com/spf13/viper"

	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	ofVersion "github.com/operator-framework/api/pkg/lib/version"
)

const (
	KeyManifestsPath   = "manifests-path"
	KeyApplicationsNS  = "applications-namespace"
	KeyPlatformName    = "platform-name"
	KeyPlatformVersion = "platform-version"

	KeyMetricsBindAddr    = "controller.metrics.bind-address"
	KeyHealthBindAddr     = "controller.health.bind-address"
	KeyLeaderElectEnabled = "controller.leader-election.enabled"
	KeyLeaderElectID      = "controller.leader-election.id"
	KeyZapLevel           = "controller.zap.level"
	KeyPprofEnabled       = "controller.pprof.enabled"
	KeyPprofBindAddr      = "controller.pprof.bind-address"

	DefaultApplicationsNS  = "opendatahub"
	DefaultPlatformName    = "unknown"
	DefaultPlatformVersion = "unknown"

	DefaultMetricsBindAddr    = ":8080"
	DefaultHealthBindAddr     = ":8081"
	DefaultLeaderElectEnabled = true
	DefaultLeaderElectID      = "opendatahub-feast-operator-lock"
	DefaultZapLevel           = "info"
	DefaultPprofEnabled       = false

	// ConfigPathEnvVar is the environment variable that points to the mounted
	// ConfigMap directory (or a single config file).
	ConfigPathEnvVar = "ODH_MODULE_OPERATOR_CONFIGURATION_PATH"

	// EnvPrefix is the prefix for environment variables that override
	// configuration values (e.g. ODH_MODULE_OPERATOR_PLATFORM_TYPE).
	EnvPrefix = "ODH_MODULE_OPERATOR"
)

// structuredExtensions is the set of file extensions that are parsed as
// structured config (YAML, JSON) rather than simple key-value pairs.
var structuredExtensions = map[string]bool{
	"yaml": true,
	"yml":  true,
	"json": true,
}

// Config holds the complete operator configuration.
//
// Values are loaded from (in order of precedence):
//  1. Struct field defaults
//  2. ConfigMap files (from ODH_MODULE_OPERATOR_CONFIGURATION_PATH)
//  3. Environment variables (ODH_MODULE_OPERATOR_ prefix)
//
// Controller-runtime fields use dot-separated ConfigMap keys under
// the "controller." prefix (e.g. "controller.leader-election.enabled").
type Config struct {
	ManifestsPath         string           `mapstructure:"manifests-path"`
	ApplicationsNamespace string           `mapstructure:"applications-namespace"`
	PlatformName          string           `mapstructure:"platform-name"`
	PlatformVersion       string           `mapstructure:"platform-version"`
	Controller            ControllerConfig `mapstructure:"controller"`
}

type ControllerConfig struct {
	Metrics        MetricsConfig        `mapstructure:"metrics"`
	Health         HealthConfig         `mapstructure:"health"`
	LeaderElection LeaderElectionConfig `mapstructure:"leader-election"`
	Zap            ZapConfig            `mapstructure:"zap"`
	Pprof          PprofConfig          `mapstructure:"pprof"`
}

type MetricsConfig struct {
	BindAddress string `mapstructure:"bind-address"`
}

type HealthConfig struct {
	BindAddress string `mapstructure:"bind-address"`
}

type LeaderElectionConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	ID      string `mapstructure:"id"`
}

type ZapConfig struct {
	Level string `mapstructure:"level"`
}

type PprofConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	BindAddress string `mapstructure:"bind-address"`
}

// Release builds a common.Release from the configured platform type and
// version. If PlatformVersion is not valid semver, the version defaults
// to 0.0.0.
func (c *Config) Release() common.Release {
	rel := common.Release{
		Name: common.Platform(c.PlatformName),
	}

	if c.PlatformVersion != "" {
		v, err := semver.ParseTolerant(c.PlatformVersion)
		if err == nil {
			rel.Version = ofVersion.OperatorVersion{Version: v}
		}
	}

	return rel
}

// Load reads operator configuration from all available sources.
//
// The loading sequence:
//  1. Set defaults
//  2. Read ConfigMap files from ODH_MODULE_OPERATOR_CONFIGURATION_PATH (if set)
//  3. Bind environment variables with the ODH_MODULE_OPERATOR_ prefix
//  4. Unmarshal into the Config struct
func Load() (*Config, error) {
	var configFS fs.FS

	if configPath := os.Getenv(ConfigPathEnvVar); configPath != "" {
		configFS = os.DirFS(configPath)
	}

	return LoadFromFS(configFS)
}

// LoadFromFS reads operator configuration from the given filesystem.
// If fsys is nil, only defaults and environment variables are used.
// This function is the primary entry point for testing.
func LoadFromFS(fsys fs.FS) (*Config, error) {
	v := viper.New()

	setDefaults(v)

	if fsys != nil {
		if err := loadFromFS(v, fsys); err != nil {
			return nil, fmt.Errorf("loading config from filesystem: %w", err)
		}
	}

	if err := bindEnv(v); err != nil {
		return nil, fmt.Errorf("binding env vars: %w", err)
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config: %w", err)
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault(KeyManifestsPath, "")
	v.SetDefault(KeyApplicationsNS, DefaultApplicationsNS)
	v.SetDefault(KeyPlatformName, DefaultPlatformName)
	v.SetDefault(KeyPlatformVersion, DefaultPlatformVersion)

	v.SetDefault(KeyMetricsBindAddr, DefaultMetricsBindAddr)
	v.SetDefault(KeyHealthBindAddr, DefaultHealthBindAddr)
	v.SetDefault(KeyLeaderElectEnabled, DefaultLeaderElectEnabled)
	v.SetDefault(KeyLeaderElectID, DefaultLeaderElectID)
	v.SetDefault(KeyZapLevel, DefaultZapLevel)
	v.SetDefault(KeyPprofEnabled, DefaultPprofEnabled)
	v.SetDefault(KeyPprofBindAddr, "")
}

func bindEnv(v *viper.Viper) error {
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	// Explicit BindEnv so Unmarshal picks up env vars.
	// AutomaticEnv only works with Get(), not Unmarshal().
	for _, key := range v.AllKeys() {
		if err := v.BindEnv(key); err != nil {
			return fmt.Errorf("binding env for key %s: %w", key, err)
		}
	}

	return nil
}

// loadFromFS reads all files from the given fs.FS into a temporary viper
// instance, then merges the result into v. Structured files (YAML/JSON)
// are parsed normally. Plain files use the filename as a dot-separated
// key path (e.g. "controller.zap.level" expands to a nested map).
// The single MergeConfigMap at the end writes to viper's config layer,
// so environment variables still take precedence.
func loadFromFS(v *viper.Viper, fsys fs.FS) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("reading config directory: %w", err)
	}

	tmp := viper.New()

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		data, err := fs.ReadFile(fsys, entry.Name())
		if err != nil {
			continue
		}

		ext := strings.TrimPrefix(filepath.Ext(entry.Name()), ".")

		if structuredExtensions[ext] {
			if err := mergeStructuredFile(tmp, entry.Name(), ext, data); err != nil {
				return err
			}
		} else {
			tmp.Set(entry.Name(), strings.TrimSpace(string(data)))
		}
	}

	if err := v.MergeConfigMap(tmp.AllSettings()); err != nil {
		return fmt.Errorf("merging config from filesystem: %w", err)
	}

	return nil
}

// mergeStructuredFile parses a YAML/JSON file and merges its keys into viper.
func mergeStructuredFile(v *viper.Viper, name string, ext string, data []byte) error {
	fv := viper.New()
	fv.SetConfigType(ext)

	if err := fv.ReadConfig(strings.NewReader(string(data))); err != nil {
		return fmt.Errorf("parsing config file %s: %w", name, err)
	}

	if err := v.MergeConfigMap(fv.AllSettings()); err != nil {
		return fmt.Errorf("merging config from %s: %w", name, err)
	}

	return nil
}
