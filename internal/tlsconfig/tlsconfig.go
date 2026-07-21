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

// Package tlsconfig provides TLS bootstrap logic for integrating with the
// OpenShift cluster-wide TLS security profile.
package tlsconfig

import (
	"context"
	"crypto/tls"
	"errors"

	configv1 "github.com/openshift/api/config/v1"
	tlspkg "github.com/openshift/controller-runtime-common/pkg/tls"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	protoH2    = "h2"
	protoHTTP1 = "http/1.1"
)

// IntermediateCiphers is the Mozilla Intermediate cipher set, used as the
// hardened default on non-OpenShift clusters where the APIServer TLS profile
// is not available.
var IntermediateCiphers = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// BootstrapResult holds the results of TLS profile bootstrap.
type BootstrapResult struct {
	TLSOpts               []func(*tls.Config)
	Profile               configv1.TLSProfileSpec
	HasOpenShiftConfigAPI bool
	TLSAdherence          configv1.TLSAdherencePolicy
	TLSAdherenceFetched   bool
	UnsupportedCiphers    []string
}

// ProfileFetcher abstracts TLS profile fetching for testability.
type ProfileFetcher interface {
	FetchTLSProfile(ctx context.Context, k8sClient client.Client) (configv1.TLSProfileSpec, error)
	FetchTLSAdherencePolicy(ctx context.Context, k8sClient client.Client) (configv1.TLSAdherencePolicy, error)
	NewTLSConfigFromProfile(profile configv1.TLSProfileSpec) (func(*tls.Config), []string)
}

// defaultFetcher delegates to the real controller-runtime-common/pkg/tls functions.
type defaultFetcher struct{}

func (defaultFetcher) FetchTLSProfile(ctx context.Context, k8sClient client.Client) (configv1.TLSProfileSpec, error) {
	return tlspkg.FetchAPIServerTLSProfile(ctx, k8sClient)
}

func (defaultFetcher) FetchTLSAdherencePolicy(ctx context.Context, k8sClient client.Client) (configv1.TLSAdherencePolicy, error) {
	return tlspkg.FetchAPIServerTLSAdherencePolicy(ctx, k8sClient)
}

func (defaultFetcher) NewTLSConfigFromProfile(profile configv1.TLSProfileSpec) (func(*tls.Config), []string) {
	return tlspkg.NewTLSConfigFromProfile(profile)
}

// DefaultFetcher returns the production ProfileFetcher that uses the real
// controller-runtime-common library.
func DefaultFetcher() ProfileFetcher {
	return defaultFetcher{}
}

// isTransientAPIError returns true for API errors that should be treated as
// recoverable during startup (watcher will self-heal when the API recovers).
func isTransientAPIError(err error) bool {
	return k8serr.IsServiceUnavailable(err) ||
		k8serr.IsTimeout(err) ||
		k8serr.IsTooManyRequests(err) ||
		errors.Is(err, context.DeadlineExceeded)
}

// Bootstrap fetches the OpenShift cluster TLS profile and adherence policy,
// classifies errors, and returns a BootstrapResult with configured TLS options.
//
// Error classification:
//   - NoMatchError (non-OpenShift): hardened defaults, no watcher
//   - NotFound (APIServer missing): hardened defaults, no watcher
//   - Transient (503/timeout/429): hardened defaults, watcher enabled for self-healing
//   - Success: profile-driven config, watcher enabled
//   - Unknown error: returned as error (caller should exit)
func Bootstrap(ctx context.Context, k8sClient client.Client, enableHTTP2 bool, fetcher ProfileFetcher) (*BootstrapResult, error) {
	nextProtos := []string{protoH2, protoHTTP1}
	if !enableHTTP2 {
		nextProtos = []string{protoHTTP1}
	}

	result := &BootstrapResult{}

	profile, err := fetcher.FetchTLSProfile(ctx, k8sClient)
	if err != nil {
		switch {
		case apimeta.IsNoMatchError(err):
			// Non-OpenShift cluster: CRD not registered.
		case k8serr.IsNotFound(err):
			// APIServer resource not found.
		case isTransientAPIError(err):
			result.HasOpenShiftConfigAPI = true
		default:
			return nil, err
		}
		result.TLSOpts = append(result.TLSOpts, func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS12
			c.CipherSuites = IntermediateCiphers
			c.NextProtos = nextProtos
		})
	} else {
		result.HasOpenShiftConfigAPI = true
		result.Profile = profile
		tlsConfigFn, unsupported := fetcher.NewTLSConfigFromProfile(profile)
		result.UnsupportedCiphers = unsupported
		result.TLSOpts = append(result.TLSOpts, tlsConfigFn, func(c *tls.Config) {
			c.NextProtos = nextProtos
		})
	}

	if result.HasOpenShiftConfigAPI {
		adherence, adherenceErr := fetcher.FetchTLSAdherencePolicy(ctx, k8sClient)
		if adherenceErr != nil {
			// Non-fatal: watcher will self-heal when the API recovers.
			result.TLSAdherence = configv1.TLSAdherencePolicyNoOpinion
		} else {
			result.TLSAdherence = adherence
		}
		result.TLSAdherenceFetched = true
	}

	return result, nil
}
