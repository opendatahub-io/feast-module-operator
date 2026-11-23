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
	"os"
	"testing"
	"testing/fstest"

	. "github.com/onsi/gomega"
)

func TestLoadCapabilityToggleDefaults(t *testing.T) {
	g := NewWithT(t)

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.FeatureStoreEnabled).To(BeTrue())
	g.Expect(cfg.DataRegistryEnabled).To(BeTrue())
}

func TestLoadCapabilityTogglesFromEnv(t *testing.T) {
	g := NewWithT(t)

	t.Setenv(EnvPrefix+"_FEATURE_STORE_ENABLED", "false")
	t.Setenv(EnvPrefix+"_DATA_REGISTRY_ENABLED", "false")

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.FeatureStoreEnabled).To(BeFalse())
	g.Expect(cfg.DataRegistryEnabled).To(BeFalse())
}

func TestLoadCapabilityTogglesUnsetEnvUsesDefaults(t *testing.T) {
	g := NewWithT(t)

	_ = os.Unsetenv(EnvPrefix + "_FEATURE_STORE_ENABLED")
	_ = os.Unsetenv(EnvPrefix + "_DATA_REGISTRY_ENABLED")

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.FeatureStoreEnabled).To(BeTrue())
	g.Expect(cfg.DataRegistryEnabled).To(BeTrue())
}

func TestLoadPlatformVersionFromEnv(t *testing.T) {
	g := NewWithT(t)

	t.Setenv(EnvPrefix+"_PLATFORM_VERSION", "2.22.0")

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformVersion).To(Equal("2.22.0"))
}

// The mounted odh-data-config ConfigMap exposes the platform version under
// the platform-managed platformVersion key (camelCase), matching what the
// opendatahub operator creates.
func TestLoadPlatformVersionFromMountedConfig(t *testing.T) {
	g := NewWithT(t)

	fsys := fstest.MapFS{
		KeyPlatformVersion: &fstest.MapFile{Data: []byte("2.22.0")},
	}

	cfg, err := LoadFromFS(fsys)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformVersion).To(Equal("2.22.0"))
}

func TestLoadPlatformVersionEnvOverridesMountedConfig(t *testing.T) {
	g := NewWithT(t)

	t.Setenv(EnvPrefix+"_PLATFORM_VERSION", "2.23.0")

	fsys := fstest.MapFS{
		KeyPlatformVersion: &fstest.MapFile{Data: []byte("2.22.0")},
	}

	cfg, err := LoadFromFS(fsys)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformVersion).To(Equal("2.23.0"))
}

func TestLoadBindsMetricsCertDirEnv(t *testing.T) {
	t.Setenv("ODH_MODULE_OPERATOR_CONTROLLER_METRICS_CERT_DIR", "/custom/certs")

	cfg, err := LoadFromFS(nil)
	if err != nil {
		t.Fatalf("LoadFromFS() error = %v", err)
	}
	if cfg.Controller.Metrics.CertDir != "/custom/certs" {
		t.Errorf("Controller.Metrics.CertDir = %q, want %q", cfg.Controller.Metrics.CertDir, "/custom/certs")
	}
}

func TestParsePlatformType(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		expected PlatformType
	}{
		{raw: "unknown", expected: PlatformTypeUnknown},
		{raw: "Open Data Hub", expected: PlatformTypeOpenDataHub},
		{raw: "OpenShift AI Self-Managed", expected: PlatformTypeSelfManagedRhoai},
		{raw: "OpenShift AI Cloud Service", expected: PlatformTypeManagedRhoai},
		{raw: "", expected: PlatformTypeUnknown},
		// Identifier forms are accepted as a safety net for emitters that
		// produce Go-style identifiers.
		{raw: "OpenDataHub", expected: PlatformTypeOpenDataHub},
		{raw: "SelfManagedRhoai", expected: PlatformTypeSelfManagedRhoai},
		{raw: "ManagedRhoai", expected: PlatformTypeManagedRhoai},
		// Everything else — including lowercase or partial variants — stays
		// Unknown.
		{raw: "open data hub", expected: PlatformTypeUnknown},
		{raw: "selfManagedRhoai", expected: PlatformTypeUnknown},
		{raw: "managedRhoai", expected: PlatformTypeUnknown},
		{raw: "odh", expected: PlatformTypeUnknown},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			g := NewWithT(t)

			g.Expect(ParsePlatformType(tc.raw)).To(Equal(tc.expected))
		})
	}
}

func TestLoadPlatformTypeFromEnv(t *testing.T) {
	g := NewWithT(t)

	t.Setenv(EnvPrefix+"_PLATFORM_TYPE", string(PlatformTypeSelfManagedRhoai))

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformType).To(Equal(PlatformTypeSelfManagedRhoai))
}

func TestLoadPlatformTypeUnsetEnvUsesDefault(t *testing.T) {
	g := NewWithT(t)

	_ = os.Unsetenv(EnvPrefix + "_PLATFORM_TYPE")

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformType).To(Equal(DefaultPlatformType))
	g.Expect(cfg.PlatformType).To(Equal(PlatformTypeOpenDataHub))
}

// Load must keep whatever raw value arrived rather than normalizing it:
// ParsePlatformType is the explicit normalizer.
func TestLoadPlatformTypeUnrecognizedValueKeptRaw(t *testing.T) {
	g := NewWithT(t)

	t.Setenv(EnvPrefix+"_PLATFORM_TYPE", "odh")

	cfg, err := LoadFromFS(nil)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(cfg.PlatformType).To(Equal(PlatformType("odh")))
	g.Expect(ParsePlatformType(string(cfg.PlatformType))).To(Equal(PlatformTypeUnknown))
}
