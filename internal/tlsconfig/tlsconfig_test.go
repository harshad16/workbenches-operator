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

package tlsconfig

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var errBoom = stderrors.New("boom")

// fakeFetcher implements ProfileFetcher for testing.
type fakeFetcher struct {
	profileResult     configv1.TLSProfileSpec
	profileErr        error
	adherenceResult   configv1.TLSAdherencePolicy
	adherenceErr      error
	profileCalled     bool
	adherenceCalled   bool
	configFromProfile func(*tls.Config)
	unsupported       []string
}

func (f *fakeFetcher) FetchTLSProfile(_ context.Context, _ client.Client) (configv1.TLSProfileSpec, error) {
	f.profileCalled = true
	return f.profileResult, f.profileErr
}

func (f *fakeFetcher) FetchTLSAdherencePolicy(_ context.Context, _ client.Client) (configv1.TLSAdherencePolicy, error) {
	f.adherenceCalled = true
	return f.adherenceResult, f.adherenceErr
}

func (f *fakeFetcher) NewTLSConfigFromProfile(_ configv1.TLSProfileSpec) (func(*tls.Config), []string) {
	fn := f.configFromProfile
	if fn == nil {
		fn = func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS12
		}
	}
	return fn, f.unsupported
}

// applyTLSOpts applies a slice of TLS option functions to a fresh tls.Config and returns it.
func applyTLSOpts(opts []func(*tls.Config)) *tls.Config {
	c := &tls.Config{}
	for _, fn := range opts {
		fn(c)
	}
	return c
}

func TestBootstrap_NonOpenShiftCluster(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
			Group: "config.openshift.io", Resource: "apiservers",
		}},
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=false for non-OpenShift cluster")
	}
	if result.TLSAdherenceFetched {
		t.Error("expected TLSAdherenceFetched=false when config API is unavailable")
	}
	if !fetcher.profileCalled {
		t.Error("expected FetchTLSProfile to be called")
	}
	if fetcher.adherenceCalled {
		t.Error("expected FetchTLSAdherencePolicy NOT to be called for non-OpenShift")
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion=TLS12, got %d", cfg.MinVersion)
	}
	if len(cfg.CipherSuites) != len(IntermediateCiphers) {
		t.Errorf("expected %d intermediate ciphers, got %d", len(IntermediateCiphers), len(cfg.CipherSuites))
	}
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != protoHTTP1 {
		t.Errorf("expected NextProtos=[http/1.1] (HTTP/2 disabled), got %v", cfg.NextProtos)
	}
}

func TestBootstrap_APIServerNotFound(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: errors.NewNotFound(schema.GroupResource{
			Group: "config.openshift.io", Resource: "apiservers",
		}, "cluster"),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=false for NotFound")
	}
	if result.TLSAdherenceFetched {
		t.Error("expected TLSAdherenceFetched=false when APIServer not found")
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion=TLS12, got %d", cfg.MinVersion)
	}
}

func TestBootstrap_TransientError_ServiceUnavailable(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: errors.NewServiceUnavailable("api down"),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=true for transient error (watcher self-healing)")
	}
	if !result.TLSAdherenceFetched {
		t.Error("expected TLSAdherenceFetched=true when config API is assumed present")
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected MinVersion=TLS12 (hardened defaults), got %d", cfg.MinVersion)
	}
	if len(cfg.CipherSuites) != len(IntermediateCiphers) {
		t.Errorf("expected intermediate cipher suite fallback, got %d ciphers", len(cfg.CipherSuites))
	}
}

func TestBootstrap_TransientError_Timeout(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: errors.NewTimeoutError("timed out", 10),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=true for timeout (watcher self-healing)")
	}
}

func TestBootstrap_TransientError_ContextDeadlineExceeded(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: fmt.Errorf("failed to get APIServer: %w", context.DeadlineExceeded),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("context.DeadlineExceeded should be transient, got fatal: %v", err)
	}

	if !result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=true for context deadline (watcher self-healing)")
	}
}

func TestBootstrap_TransientError_TooManyRequests(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: errors.NewTooManyRequestsError("throttled"),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=true for TooManyRequests (watcher self-healing)")
	}
}

func TestBootstrap_UnknownError_Fatal(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: fmt.Errorf("unexpected: %w", errors.NewInternalError(errBoom)),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err == nil {
		t.Fatal("expected error for unknown API error, got nil")
	}
	if result != nil {
		t.Errorf("expected nil result on fatal error, got %+v", result)
	}
}

func TestBootstrap_Success_OpenShift(t *testing.T) {
	profileSpec := configv1.TLSProfileSpec{
		Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
		MinTLSVersion: configv1.VersionTLS12,
	}

	appliedMinVersion := uint16(0)
	fetcher := &fakeFetcher{
		profileResult:   profileSpec,
		adherenceResult: configv1.TLSAdherencePolicyStrictAllComponents,
		configFromProfile: func(c *tls.Config) {
			c.MinVersion = tls.VersionTLS12
			appliedMinVersion = c.MinVersion
		},
	}

	result, err := Bootstrap(context.Background(), nil, true, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.HasOpenShiftConfigAPI {
		t.Error("expected HasOpenShiftConfigAPI=true on success")
	}
	if result.Profile.MinTLSVersion != configv1.VersionTLS12 {
		t.Errorf("expected profile to be stored, got MinTLSVersion=%s", result.Profile.MinTLSVersion)
	}
	if !result.TLSAdherenceFetched {
		t.Error("expected TLSAdherenceFetched=true")
	}
	if result.TLSAdherence != configv1.TLSAdherencePolicyStrictAllComponents {
		t.Errorf("expected TLSAdherence=StrictAllComponents, got %q", result.TLSAdherence)
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if appliedMinVersion != tls.VersionTLS12 {
		t.Error("expected profile-driven TLS config function to be applied")
	}
	// HTTP/2 enabled: expect both h2 and http/1.1
	if len(cfg.NextProtos) != 2 || cfg.NextProtos[0] != protoH2 || cfg.NextProtos[1] != protoHTTP1 {
		t.Errorf("expected NextProtos=[h2, http/1.1] (HTTP/2 enabled), got %v", cfg.NextProtos)
	}
}

func TestBootstrap_Success_UnsupportedCiphers(t *testing.T) {
	fetcher := &fakeFetcher{
		profileResult: configv1.TLSProfileSpec{MinTLSVersion: configv1.VersionTLS12},
		unsupported:   []string{"FAKE_CIPHER_1", "FAKE_CIPHER_2"},
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.UnsupportedCiphers) != 2 {
		t.Errorf("expected 2 unsupported ciphers, got %d", len(result.UnsupportedCiphers))
	}
}

func TestBootstrap_NextProtos_HTTP2Enabled(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
			Group: "config.openshift.io", Resource: "apiservers",
		}},
	}

	result, err := Bootstrap(context.Background(), nil, true, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if len(cfg.NextProtos) != 2 || cfg.NextProtos[0] != protoH2 || cfg.NextProtos[1] != protoHTTP1 {
		t.Errorf("expected NextProtos=[h2, http/1.1], got %v", cfg.NextProtos)
	}
}

func TestBootstrap_NextProtos_HTTP2Disabled(t *testing.T) {
	fetcher := &fakeFetcher{
		profileErr: &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{
			Group: "config.openshift.io", Resource: "apiservers",
		}},
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg := applyTLSOpts(result.TLSOpts)
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != protoHTTP1 {
		t.Errorf("expected NextProtos=[http/1.1], got %v", cfg.NextProtos)
	}
}

func TestBootstrap_AdherenceFetchFailure_NonFatal(t *testing.T) {
	fetcher := &fakeFetcher{
		profileResult: configv1.TLSProfileSpec{MinTLSVersion: configv1.VersionTLS12},
		adherenceErr:  errors.NewServiceUnavailable("adherence API down"),
	}

	result, err := Bootstrap(context.Background(), nil, false, fetcher)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.TLSAdherenceFetched {
		t.Error("expected TLSAdherenceFetched=true even on adherence fetch failure")
	}
	if result.TLSAdherence != configv1.TLSAdherencePolicyNoOpinion {
		t.Errorf("expected TLSAdherence=NoOpinion on fetch failure, got %q", result.TLSAdherence)
	}
}

func TestBootstrap_HardenedDefaults_AllCiphersAreAEAD(t *testing.T) {
	for _, cipher := range IntermediateCiphers {
		name := tls.CipherSuiteName(cipher)
		if name == "" {
			t.Errorf("cipher 0x%04x is not recognized by Go's TLS library", cipher)
			continue
		}
		// All intermediate ciphers must be AEAD (GCM or ChaCha20-Poly1305)
		found := false
		for _, suite := range tls.CipherSuites() {
			if suite.ID == cipher {
				found = true
				if suite.Insecure {
					t.Errorf("cipher %s (0x%04x) is marked insecure", name, cipher)
				}
				break
			}
		}
		if !found {
			t.Errorf("cipher %s (0x%04x) not in tls.CipherSuites() (may be insecure)", name, cipher)
		}
	}
}

func TestBootstrap_TransientError_AllTypesClassifiedCorrectly(t *testing.T) {
	transientErrors := []struct {
		name string
		err  error
	}{
		{"ServiceUnavailable", errors.NewServiceUnavailable("down")},
		{"Timeout", errors.NewTimeoutError("slow", 5)},
		{"TooManyRequests", errors.NewTooManyRequestsError("throttled")},
		{"ContextDeadlineExceeded", context.DeadlineExceeded},
	}

	for _, tc := range transientErrors {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &fakeFetcher{profileErr: tc.err}
			result, err := Bootstrap(context.Background(), nil, false, fetcher)
			if err != nil {
				t.Fatalf("transient error %s should not be fatal: %v", tc.name, err)
			}
			if !result.HasOpenShiftConfigAPI {
				t.Errorf("transient error %s should set HasOpenShiftConfigAPI=true", tc.name)
			}
		})
	}
}

func TestBootstrap_NonTransientErrors_AreFatal(t *testing.T) {
	fatalErrors := []struct {
		name string
		err  error
	}{
		{"InternalError", errors.NewInternalError(errBoom)},
		{"Forbidden", errors.NewForbidden(schema.GroupResource{Resource: "apiservers"}, "cluster", stderrors.New("no access"))},
		{"Unauthorized", errors.NewUnauthorized("bad creds")},
		{"GenericStatusError", &errors.StatusError{ErrStatus: metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusBadGateway,
			Reason:  metav1.StatusReasonUnknown,
			Message: "bad gateway",
		}}},
	}

	for _, tc := range fatalErrors {
		t.Run(tc.name, func(t *testing.T) {
			fetcher := &fakeFetcher{profileErr: tc.err}
			result, err := Bootstrap(context.Background(), nil, false, fetcher)
			if err == nil {
				t.Fatalf("error %s should be fatal, got nil error", tc.name)
			}
			if result != nil {
				t.Errorf("expected nil result on fatal error %s", tc.name)
			}
		})
	}
}
