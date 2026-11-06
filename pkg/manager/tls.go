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
	"errors"
	"fmt"
	"net"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	ocpcrypto "github.com/openshift/library-go/pkg/crypto"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	frameworktls "github.com/opendatahub-io/odh-platform-utilities/framework/tls"
	commonTLS "github.com/openshift/controller-runtime-common/pkg/tls"
)

const bootstrapFallbackTLSAdherence configv1.TLSAdherencePolicy = "BootstrapFallback"

func registerTLSProfileWatcher(mgr ctrl.Manager, result frameworktls.LoadResult) error {
	if !result.Watchable {
		return nil
	}

	tlsChanges := make(chan struct{}, 1)
	restart := func() {
		select {
		case tlsChanges <- struct{}{}:
		default:
		}
	}
	initialAdherence := tlsWatcherInitialAdherence(result)
	watcher := &frameworktls.SecurityProfileWatcher{
		Client:                    mgr.GetClient(),
		InitialTLSProfileSpec:     result.Spec,
		InitialTLSAdherencePolicy: initialAdherence,
		OnProfileChange: func(context.Context, configv1.TLSProfileSpec, configv1.TLSProfileSpec) {
			ctrl.Log.WithName("setup").Info("cluster TLS profile changed, restarting manager")
			restart()
		},
		OnAdherencePolicyChange: func(context.Context, configv1.TLSAdherencePolicy, configv1.TLSAdherencePolicy) {
			ctrl.Log.WithName("setup").Info("cluster TLS adherence policy changed, restarting manager")
			restart()
		},
	}
	if err := watcher.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("registering TLS profile watcher: %w", err)
	}
	if err := mgr.Add(tlsRestartRunnable{changes: tlsChanges}); err != nil {
		return fmt.Errorf("registering TLS restart signal: %w", err)
	}
	return nil
}

func tlsWatcherInitialAdherence(result frameworktls.LoadResult) configv1.TLSAdherencePolicy {
	if result.UsedFallback && result.Watchable {
		return bootstrapFallbackTLSAdherence
	}
	return result.AdherencePolicy
}

type tlsRestartRunnable struct {
	changes <-chan struct{}
}

func (r tlsRestartRunnable) Start(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil
	case <-r.changes:
		return fmt.Errorf("cluster TLS configuration changed, restart required")
	}
}

func (tlsRestartRunnable) NeedLeaderElection() bool {
	return false
}

func metricsTLSOptions(result frameworktls.LoadResult) ([]func(*tls.Config), error) {
	if result.UsedFallback && !result.Watchable {
		return nil, nil
	}

	if !result.UsedFallback && !honorsClusterTLSProfile(result.AdherencePolicy) {
		return nil, nil
	}

	profileTLSOpt, unsupported, err := strictProfileTLSConfig(result.Spec)
	if err != nil {
		return nil, err
	}
	if len(unsupported) > 0 {
		ctrl.Log.WithName("setup").Info("dropping unsupported TLS profile settings", "settings", unsupported)
	}

	resolved := &tls.Config{}
	profileTLSOpt(resolved)
	protocols := commonTLS.HTTP1NextProtos
	if resolved.MinVersion >= tls.VersionTLS12 && hasAES128GCMSuite(result.Spec, resolved) {
		protocols = commonTLS.HTTP2NextProtos
	}

	return []func(*tls.Config){profileTLSOpt, commonTLS.SetNextProtos(protocols...)}, nil
}

func loadTLSProfile(ctx context.Context, reader client.Reader) (frameworktls.LoadResult, error) {
	apiServer := &configv1.APIServer{}
	err := reader.Get(ctx, client.ObjectKey{Name: commonTLS.APIServerName}, apiServer)
	if err != nil {
		fallback := frameworktls.LoadResult{
			Spec:            *frameworktls.ProfileSpecFromSecurityProfile(nil),
			AdherencePolicy: configv1.TLSAdherencePolicyNoOpinion,
			UsedFallback:    true,
		}

		switch {
		case meta.IsNoMatchError(err):
			return fallback, nil
		case k8serrors.IsNotFound(err), isTransientTLSReadError(err):
			fallback.Watchable = true
			return fallback, nil
		default:
			return frameworktls.LoadResult{}, fmt.Errorf("getting APIServer TLS profile: %w", err)
		}
	}

	profile := frameworktls.ProfileSpecFromSecurityProfile(apiServer.Spec.TLSSecurityProfile)
	if honorsClusterTLSProfile(apiServer.Spec.TLSAdherence) {
		if apiServer.Spec.TLSAdherence != configv1.TLSAdherencePolicyStrictAllComponents {
			ctrl.Log.WithName("setup").Info(
				"unknown TLS adherence policy, treating it as StrictAllComponents",
				"adherence", apiServer.Spec.TLSAdherence,
			)
		}
		strictProfile, err := strictTLSProfileSpec(apiServer.Spec.TLSSecurityProfile)
		if err != nil {
			return frameworktls.LoadResult{}, fmt.Errorf("invalid strict APIServer TLS profile: %w", err)
		}
		if _, _, err := strictProfileTLSConfig(strictProfile); err != nil {
			return frameworktls.LoadResult{}, fmt.Errorf("invalid strict APIServer TLS profile: %w", err)
		}
		profile = &strictProfile
	}

	return frameworktls.LoadResult{
		Spec:            *profile,
		AdherencePolicy: apiServer.Spec.TLSAdherence,
		Watchable:       true,
	}, nil
}

func honorsClusterTLSProfile(adherence configv1.TLSAdherencePolicy) bool {
	switch adherence {
	case configv1.TLSAdherencePolicyNoOpinion, configv1.TLSAdherencePolicyLegacyAdheringComponentsOnly:
		return false
	default:
		return true
	}
}

func strictTLSProfileSpec(profile *configv1.TLSSecurityProfile) (configv1.TLSProfileSpec, error) {
	if profile == nil || profile.Type == "" {
		return *configv1.TLSProfiles[configv1.TLSProfileIntermediateType], nil
	}
	if profile.Type == configv1.TLSProfileCustomType {
		if profile.Custom == nil {
			return configv1.TLSProfileSpec{}, fmt.Errorf("custom TLS profile has no custom settings")
		}
		return profile.Custom.TLSProfileSpec, nil
	}

	spec, ok := configv1.TLSProfiles[profile.Type]
	if !ok || spec == nil {
		return configv1.TLSProfileSpec{}, fmt.Errorf("unsupported TLS profile type %q", profile.Type)
	}
	return *spec, nil
}

func isTransientTLSReadError(err error) bool {
	// Explicitly transient API statuses first.
	if k8serrors.IsServiceUnavailable(err) ||
		k8serrors.IsTimeout(err) ||
		k8serrors.IsServerTimeout(err) ||
		k8serrors.IsTooManyRequests(err) ||
		k8serrors.IsInternalError(err) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Reject other API-status errors before checking transport predicates.
	var statusErr *k8serrors.StatusError
	if errors.As(err, &statusErr) {
		return false
	}
	// Transport-level errors for non-API-status failures.
	var netErr net.Error
	return utilnet.IsConnectionRefused(err) ||
		utilnet.IsConnectionReset(err) ||
		utilnet.IsProbableEOF(err) ||
		utilnet.IsHTTP2ConnectionLost(err) ||
		(errors.As(err, &netErr) && netErr.Timeout())
}

func hasAES128GCMSuite(profile configv1.TLSProfileSpec, resolved *tls.Config) bool {
	if profile.MinTLSVersion == configv1.VersionTLS13 {
		// TLS 1.3 suites are runtime-managed in Go and include an HTTP/2-
		// compatible AES-128-GCM suite even when the profile cipher list is empty.
		if len(profile.Ciphers) == 0 {
			return true
		}
		for _, cipher := range profile.Ciphers {
			if code, ok := cipherSuiteID(cipher); ok && strings.Contains(tls.CipherSuiteName(code), "AES_128_GCM") {
				return true
			}
		}
		return false
	}
	for _, cipher := range resolved.CipherSuites {
		if strings.Contains(tls.CipherSuiteName(cipher), "AES_128_GCM") {
			return true
		}
	}
	return false
}

func strictProfileTLSConfig(profile configv1.TLSProfileSpec) (func(*tls.Config), []string, error) {
	minimum, ok := tlsMinimumVersion(profile.MinTLSVersion)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported TLS minimum version %q", profile.MinTLSVersion)
	}

	var curvePreferences []tls.CurveID
	var unsupportedGroups []string
	if len(profile.Groups) > 0 {
		var dropped []configv1.TLSGroup
		curvePreferences, dropped = ocpcrypto.TLSGroupsToCurveIDs(profile.Groups)
		for _, group := range dropped {
			unsupportedGroups = append(unsupportedGroups, string(group))
		}
		if len(curvePreferences) == 0 {
			return nil, nil, fmt.Errorf("TLS profile has no usable groups: %v", profile.Groups)
		}
	}

	profileForBaseTLSOpt := profile
	profileForBaseTLSOpt.Ciphers = nil
	profileForBaseTLSOpt.Groups = nil
	baseTLSOpt, unsupported := frameworktls.ConfigFromProfile(profileForBaseTLSOpt)
	unsupported = append(unsupported, unsupportedGroups...)
	var cipherSuites []uint16
	if minimum == tls.VersionTLS13 {
		usable := len(profile.Ciphers) == 0
		for _, cipher := range profile.Ciphers {
			code, _ := cipherSuiteID(cipher)
			switch code {
			case tls.TLS_AES_128_GCM_SHA256, tls.TLS_AES_256_GCM_SHA384, tls.TLS_CHACHA20_POLY1305_SHA256:
				usable = true
			default:
				unsupported = append(unsupported, cipher)
			}
		}
		if !usable {
			return nil, nil, fmt.Errorf("TLS profile has no cipher suites supported by Go")
		}
	} else {
		if len(profile.Ciphers) == 0 {
			return nil, nil, fmt.Errorf("TLS profile has no cipher suites")
		}

		supported := supportedCipherSuiteIDs()
		for _, cipher := range profile.Ciphers {
			code, found := cipherSuiteID(cipher)
			if found && code&0xff00 != 0x1300 && supported[code] {
				cipherSuites = append(cipherSuites, code)
				continue
			}
			unsupported = append(unsupported, cipher)
		}
		if len(cipherSuites) == 0 {
			return nil, nil, fmt.Errorf("TLS profile has no cipher suites supported by Go")
		}
	}

	return func(config *tls.Config) {
		baseTLSOpt(config)
		config.MinVersion = minimum
		if minimum == tls.VersionTLS13 {
			config.CipherSuites = nil
		} else {
			config.CipherSuites = append([]uint16(nil), cipherSuites...)
		}
		if len(curvePreferences) > 0 {
			config.CurvePreferences = append([]tls.CurveID(nil), curvePreferences...)
		}
	}, unsupported, nil
}

func tlsMinimumVersion(version configv1.TLSProtocolVersion) (uint16, bool) {
	switch version {
	case configv1.VersionTLS10:
		return tls.VersionTLS10, true
	case configv1.VersionTLS11:
		return tls.VersionTLS11, true
	case configv1.VersionTLS12:
		return tls.VersionTLS12, true
	case configv1.VersionTLS13:
		return tls.VersionTLS13, true
	default:
		return 0, false
	}
}

func cipherSuiteID(cipher string) (uint16, bool) {
	if code, err := ocpcrypto.CipherSuite(cipher); err == nil {
		return code, true
	}
	ianaCiphers := ocpcrypto.OpenSSLToIANACipherSuites([]string{cipher})
	if len(ianaCiphers) != 1 {
		return 0, false
	}
	code, err := ocpcrypto.CipherSuite(ianaCiphers[0])
	return code, err == nil
}

func supportedCipherSuiteIDs() map[uint16]bool {
	supported := make(map[uint16]bool)
	for _, cipher := range tls.CipherSuites() {
		supported[cipher.ID] = true
	}
	for _, cipher := range tls.InsecureCipherSuites() {
		supported[cipher.ID] = true
	}
	return supported
}
