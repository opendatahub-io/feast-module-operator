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
	"testing"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	componentApi "github.com/opendatahub-io/feast-module-operator/api/components/v1alpha1"
	moduleconfig "github.com/opendatahub-io/feast-module-operator/pkg/config"
	"github.com/opendatahub-io/opendatahub-operator/v2/api/common"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/cluster"
	odhtypes "github.com/opendatahub-io/opendatahub-operator/v2/pkg/controller/types"
)

func newTestModule(t *testing.T) *Module {
	t.Helper()

	cfg := &moduleconfig.Config{
		PlatformName:          string(cluster.OpenDataHub),
		PlatformVersion:       "1.0.0",
		ManifestsPath:         "/manifests",
		ApplicationsNamespace: "test-ns",
	}

	m, err := NewModule(cfg)
	NewWithT(t).Expect(err).NotTo(HaveOccurred())

	return m
}

func newTestRR(obj *componentApi.FeastOperator) *odhtypes.ReconciliationRequest {
	return &odhtypes.ReconciliationRequest{
		Instance:          obj,
		ManifestsBasePath: "/manifests",
		Release: (&moduleconfig.Config{
			PlatformName:    string(cluster.OpenDataHub),
			PlatformVersion: "1.0.0",
		}).Release(),
	}
}

func newTestFeastOperator() *componentApi.FeastOperator {
	return &componentApi.FeastOperator{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentApi.FeastOperatorInstanceName,
		},
	}
}

func TestNewModule(t *testing.T) {
	g := NewWithT(t)

	cfg := &moduleconfig.Config{
		PlatformName:    string(cluster.OpenDataHub),
		PlatformVersion: "1.0.0",
		ManifestsPath:   "/manifests",
	}

	m, err := NewModule(cfg)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(m.cfg).To(Equal(cfg))
	g.Expect(m.manifestInfo.Path).To(Equal(cfg.ManifestsPath))
	g.Expect(m.manifestInfo.ContextDir).To(Equal(componentName))
	g.Expect(m.manifestInfo.SourcePath).To(Equal(overlayODH))
}

func TestInitialize(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	rr := newTestRR(obj)

	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())
	g.Expect(rr.Manifests).To(HaveLen(1))
	g.Expect(rr.Manifests[0].Path).To(Equal("/manifests"))
	g.Expect(rr.Manifests[0].ContextDir).To(Equal(componentName))
	g.Expect(rr.Manifests[0].SourcePath).To(Equal(overlayODH))
}

func TestUpgradeIfNeededFreshInstall(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestUpgradeIfNeededSameVersion(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	setPlatformRelease(obj, "1.0.0")
	rr := newTestRR(obj)

	g.Expect(m.upgradeIfNeeded(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsNoOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	// No OIDC set on the CR
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	// Should succeed and write empty OIDC_ISSUER_URL
	g.Expect(m.setKustomizedParams(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsWithOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	obj.Spec.OIDC = &common.GatewayOIDCSpec{IssuerURL: "https://issuer.example.com"}
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	g.Expect(m.setKustomizedParams(context.Background(), rr)).To(Succeed())
}

func TestSetKustomizedParamsInvalidOIDC(t *testing.T) {
	g := NewWithT(t)

	m := newTestModule(t)
	obj := newTestFeastOperator()
	obj.Spec.OIDC = &common.GatewayOIDCSpec{IssuerURL: "not-a-url"}
	rr := newTestRR(obj)
	g.Expect(m.initialize(context.Background(), rr)).To(Succeed())

	err := m.setKustomizedParams(context.Background(), rr)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("invalid OIDC issuer URL"))
}

func TestGetSetPlatformRelease(t *testing.T) {
	g := NewWithT(t)

	obj := newTestFeastOperator()

	// Initially empty
	g.Expect(getPlatformRelease(obj)).To(Equal(""))

	// Set a version
	setPlatformRelease(obj, "2.20.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.20.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(1))
	g.Expect(obj.Status.Releases[0].Name).To(Equal("platform"))

	// Update the version
	setPlatformRelease(obj, "2.21.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.21.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(1))

	// Simulate releases.NewAction() overwriting status.releases with
	// component releases, then setPlatformRelease appending the platform
	// entry — this mirrors the actual controller action ordering.
	obj.SetReleaseStatus([]common.ComponentRelease{
		{Name: "Feast", Version: "0.64.0"},
	})
	g.Expect(getPlatformRelease(obj)).To(Equal(""))
	setPlatformRelease(obj, "2.21.0")
	g.Expect(getPlatformRelease(obj)).To(Equal("2.21.0"))
	g.Expect(obj.Status.Releases).To(HaveLen(2))
}

func TestParseAndValidateOIDCIssuerURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{"valid https", "https://issuer.example.com", false, ""},
		{"valid https with path", "https://issuer.example.com/path", false, ""},
		{"http not allowed", "http://issuer.example.com", true, "https scheme"},
		{"no host", "https://", true, "host"},
		{"not a url", "not-a-url", true, ""},
		{"empty", "", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := parseAndValidateOIDCIssuerURL(tt.input)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				if tt.errMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errMsg))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
