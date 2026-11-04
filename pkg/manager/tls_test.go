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

package manager

import (
	"context"
	"crypto/tls"
	"fmt"
	"testing"

	frameworktls "github.com/opendatahub-io/odh-platform-utilities/framework/tls"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestMetricsTLSOptions(t *testing.T) {
	t.Parallel()

	t.Run("no opinion uses component defaults", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            *configv1.TLSProfiles[configv1.TLSProfileOldType],
			AdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
			Watchable:       true,
		})

		require.NoError(t, err)
		require.Empty(t, options)
	})

	t.Run("legacy adherence uses component defaults", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            *configv1.TLSProfiles[configv1.TLSProfileOldType],
			AdherencePolicy: configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly,
			Watchable:       true,
		})

		require.NoError(t, err)
		require.Empty(t, options)
	})

	t.Run("transient fallback applies intermediate profile", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:         *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
			UsedFallback: true,
			Watchable:    true,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, uint16(tls.VersionTLS12), resolved.MinVersion)
		require.Contains(t, resolved.NextProtos, "h2")
		require.NotEmpty(t, resolved.CipherSuites)
	})

	t.Run("unserved API keeps Go defaults", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:         *configv1.TLSProfiles[configv1.TLSProfileIntermediateType],
			UsedFallback: true,
			Watchable:    false,
		})

		require.NoError(t, err)
		require.Empty(t, options)
	})

	t.Run("TLS 1.3 has no configurable cipher suites", func(t *testing.T) {
		t.Parallel()

		profile := configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS13,
			Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
		}
		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            profile,
			AdherencePolicy: configv1.TLSAdherencePolicy("FuturePolicy"),
			Watchable:       true,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, uint16(tls.VersionTLS13), resolved.MinVersion)
		require.Nil(t, resolved.CipherSuites)
		require.Equal(t, []string{"h2", "http/1.1"}, resolved.NextProtos)
	})

	t.Run("TLS 1.2 accepts IANA cipher names", func(t *testing.T) {
		t.Parallel()

		profile := configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS12,
			Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
		}
		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            profile,
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
			Watchable:       true,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, []string{"h2", "http/1.1"}, resolved.NextProtos)
		require.Equal(t, []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}, resolved.CipherSuites)
	})

	t.Run("old profile retains TLS 1.0 minimum", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            *configv1.TLSProfiles[configv1.TLSProfileOldType],
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
			Watchable:       true,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, uint16(tls.VersionTLS10), resolved.MinVersion)
		require.Equal(t, []string{"http/1.1"}, resolved.NextProtos)
		require.NotEmpty(t, resolved.CipherSuites)
	})

	t.Run("TLS 1.2 without AES 128 GCM disables HTTP 2", func(t *testing.T) {
		t.Parallel()

		profile := configv1.TLSProfileSpec{
			MinTLSVersion: configv1.VersionTLS12,
			Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"},
		}
		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec:            profile,
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
			Watchable:       true,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, []string{"http/1.1"}, resolved.NextProtos)
	})

	t.Run("TLS 1.2 without usable ciphers fails", func(t *testing.T) {
		t.Parallel()

		_, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS12,
				Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
			},
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
		})

		require.Error(t, err)
	})

	t.Run("TLS 1.3 empty cipher list keeps suites unset", func(t *testing.T) {
		t.Parallel()

		options, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS13,
			},
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
		})

		require.NoError(t, err)
		resolved := &tls.Config{}
		for _, option := range options {
			option(resolved)
		}
		require.Equal(t, uint16(tls.VersionTLS13), resolved.MinVersion)
		require.Nil(t, resolved.CipherSuites)
	})

	t.Run("TLS 1.3 with no usable ciphers fails", func(t *testing.T) {
		t.Parallel()

		_, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec: configv1.TLSProfileSpec{
				MinTLSVersion: configv1.VersionTLS13,
				Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
			},
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
		})

		require.Error(t, err)
	})

	t.Run("strict invalid profile fails", func(t *testing.T) {
		t.Parallel()

		_, err := metricsTLSOptions(frameworktls.LoadResult{
			Spec: configv1.TLSProfileSpec{
				MinTLSVersion: "unknown",
			},
			AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
			Watchable:       true,
		})

		require.Error(t, err)
	})
}

func TestLoadTLSProfileFallbacks(t *testing.T) {
	t.Parallel()

	groupResource := schema.GroupResource{Group: "config.openshift.io", Resource: "apiservers"}
	tests := []struct {
		name          string
		err           error
		wantWatchable bool
		wantErr       bool
	}{
		{
			name:          "NotFound remains watchable",
			err:           apierrors.NewNotFound(groupResource, "cluster"),
			wantWatchable: true,
		},
		{
			name: "NoMatch does not watch",
			err: &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
				Group: groupResource.Group, Resource: groupResource.Resource,
			}},
		},
		{
			name:          "transient remains watchable",
			err:           apierrors.NewServiceUnavailable("unavailable"),
			wantWatchable: true,
		},
		{
			name:    "forbidden fails closed",
			err:     apierrors.NewForbidden(groupResource, "cluster", fmt.Errorf("denied")),
			wantErr: true,
		},
		{
			name:    "unauthorized fails closed",
			err:     apierrors.NewUnauthorized("unauthorized"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := loadTLSProfile(context.Background(), tlsProfileReader{err: tt.err})
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.True(t, result.UsedFallback)
			require.Equal(t, tt.wantWatchable, result.Watchable)
			require.Equal(t, configv1.VersionTLS12, result.Spec.MinTLSVersion)
		})
	}
}

func TestTLSWatcherInitialAdherence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result frameworktls.LoadResult
		want   configv1.TLSAdherencePolicy
	}{
		{
			name: "watchable fallback forces recovery restart",
			result: frameworktls.LoadResult{
				AdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
				UsedFallback:    true,
				Watchable:       true,
			},
			want: bootstrapFallbackTLSAdherence,
		},
		{
			name: "successful read uses actual adherence",
			result: frameworktls.LoadResult{
				AdherencePolicy: configv1.TLSAdherencePolicyStrictAllComponents,
				Watchable:       true,
			},
			want: configv1.TLSAdherencePolicyStrictAllComponents,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tlsWatcherInitialAdherence(tt.result))
		})
	}
}

func TestLoadTLSProfileValidation(t *testing.T) {
	t.Parallel()

	t.Run("missing profile resolves to intermediate", func(t *testing.T) {
		t.Parallel()

		result, err := loadTLSProfile(context.Background(), tlsProfileReader{
			server: &configv1.APIServer{Spec: configv1.APIServerSpec{
				TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
			}},
		})

		require.NoError(t, err)
		require.Equal(t, configv1.VersionTLS12, result.Spec.MinTLSVersion)
		require.True(t, result.Watchable)
	})

	t.Run("strict IANA cipher profile loads", func(t *testing.T) {
		t.Parallel()

		result, err := loadTLSProfile(context.Background(), tlsProfileReader{
			server: &configv1.APIServer{Spec: configv1.APIServerSpec{
				TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileCustomType,
					Custom: &configv1.CustomTLSProfile{TLSProfileSpec: configv1.TLSProfileSpec{
						MinTLSVersion: configv1.VersionTLS12,
						Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
					}},
				},
			}},
		})

		require.NoError(t, err)
		require.Equal(t, []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}, result.Spec.Ciphers)
	})

	t.Run("unknown profile type is strict for unknown adherence", func(t *testing.T) {
		t.Parallel()

		_, err := loadTLSProfile(context.Background(), tlsProfileReader{
			server: &configv1.APIServer{Spec: configv1.APIServerSpec{
				TLSAdherence:       configv1.TLSAdherencePolicy("FuturePolicy"),
				TLSSecurityProfile: &configv1.TLSSecurityProfile{Type: "FutureProfile"},
			}},
		})

		require.Error(t, err)
	})

	t.Run("custom profile without settings is strict error", func(t *testing.T) {
		t.Parallel()

		_, err := loadTLSProfile(context.Background(), tlsProfileReader{
			server: &configv1.APIServer{Spec: configv1.APIServerSpec{
				TLSAdherence: configv1.TLSAdherencePolicyStrictAllComponents,
				TLSSecurityProfile: &configv1.TLSSecurityProfile{
					Type: configv1.TLSProfileCustomType,
				},
			}},
		})

		require.Error(t, err)
	})
}

type tlsProfileReader struct {
	err    error
	server *configv1.APIServer
}

func (r tlsProfileReader) Get(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	if r.err != nil {
		return r.err
	}
	r.server.DeepCopyInto(obj.(*configv1.APIServer))
	return nil
}

func (tlsProfileReader) List(context.Context, client.ObjectList, ...client.ListOption) error {
	return nil
}
