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

package platformconfig

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/platform"
)

func TestReadDesiredDistribution(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "opendatahub",
		},
		Data: map[string]string{
			DistributionNameKey:    " OpenDataHub ",
			DistributionVersionKey: " 3.5.1 ",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

	got, err := ReadDesiredDistribution(context.Background(), cli, "opendatahub")
	if err != nil {
		t.Fatalf("ReadDesiredDistribution() error = %v", err)
	}

	if got.Name != "OpenDataHub" || got.Version != "3.5.1" {
		t.Fatalf("ReadDesiredDistribution() = %#v, want OpenDataHub/3.5.1", got)
	}
}

func TestReadDesiredDistributionMissingConfigMap(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	got, err := ReadDesiredDistribution(context.Background(), cli, "opendatahub")
	if err != nil {
		t.Fatalf("ReadDesiredDistribution() error = %v", err)
	}

	if !IsDistributionEmpty(got) {
		t.Fatalf("ReadDesiredDistribution() = %#v, want empty", got)
	}
}

func TestReadDesiredDistributionPartialKeys(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "opendatahub",
		},
		Data: map[string]string{
			DistributionVersionKey: "3.5.1",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

	got, err := ReadDesiredDistribution(context.Background(), cli, "opendatahub")
	if err != nil {
		t.Fatalf("ReadDesiredDistribution() error = %v", err)
	}

	if got.Name != "" || got.Version != "3.5.1" {
		t.Fatalf("ReadDesiredDistribution() = %#v, want empty name and version 3.5.1", got)
	}
}

func TestDistributionAligned(t *testing.T) {
	t.Parallel()

	desired := componentsv1alpha1.Distribution{Name: "OpenDataHub", Version: "3.5.1"}

	if !DistributionAligned(desired, desired) {
		t.Fatal("DistributionAligned() = false, want true for matching values")
	}

	if DistributionAligned(desired, componentsv1alpha1.Distribution{}) {
		t.Fatal("DistributionAligned() = true, want false for empty current")
	}

	if DistributionAligned(desired, componentsv1alpha1.Distribution{Name: "OpenDataHub", Version: "3.4.0"}) {
		t.Fatal("DistributionAligned() = true, want false for version mismatch")
	}
}

func TestStandaloneDistribution(t *testing.T) {
	t.Parallel()

	got := StandaloneDistribution("")
	if got.Name != DistributionNameStandalone || got.Version != "0.0.0" {
		t.Fatalf("StandaloneDistribution(\"\") = %#v, want Standalone/0.0.0", got)
	}

	got = StandaloneDistribution("1.2.3")
	if got.Name != DistributionNameStandalone || got.Version != "1.2.3" {
		t.Fatalf("StandaloneDistribution(\"1.2.3\") = %#v", got)
	}

	got = StandaloneDistribution("1.2.3\nspoofed")
	if got.Name != DistributionNameStandalone || got.Version != "0.0.0" {
		t.Fatalf("StandaloneDistribution(control chars) = %#v, want Standalone/0.0.0", got)
	}
}

func TestResolveDesiredDistribution(t *testing.T) {
	t.Parallel()

	standalone := ResolveDesiredDistribution(componentsv1alpha1.Distribution{}, "", "1.0.0")
	if standalone.Name != DistributionNameStandalone || standalone.Version != "1.0.0" {
		t.Fatalf("ResolveDesiredDistribution() standalone = %#v", standalone)
	}

	fromSpec := ResolveDesiredDistribution(
		componentsv1alpha1.Distribution{Version: "3.5.1"},
		platform.SelfManagedRhoai,
		"1.0.0",
	)
	if fromSpec.Name != DistributionNameSelfManagedRHOAI || fromSpec.Version != "3.5.1" {
		t.Fatalf("ResolveDesiredDistribution() from spec = %#v", fromSpec)
	}

	versionOnly := ResolveDesiredDistribution(
		componentsv1alpha1.Distribution{Version: "3.5.1"},
		"",
		"",
	)
	if versionOnly.Name != DistributionNameStandalone || versionOnly.Version != "3.5.1" {
		t.Fatalf("ResolveDesiredDistribution() version-only = %#v", versionOnly)
	}

	nameOnly := ResolveDesiredDistribution(
		componentsv1alpha1.Distribution{Name: platform.OpenDataHub},
		"",
		"",
	)
	if nameOnly.Name != platform.OpenDataHub || nameOnly.Version != "0.0.0" {
		t.Fatalf("ResolveDesiredDistribution() name-only = %#v", nameOnly)
	}
}

func TestDistributionNameFromPlatform(t *testing.T) {
	t.Parallel()

	if got := DistributionNameFromPlatform(platform.OpenDataHub); got != platform.OpenDataHub {
		t.Fatalf("DistributionNameFromPlatform(OpenDataHub) = %q", got)
	}

	if got := DistributionNameFromPlatform(platform.SelfManagedRhoai); got != DistributionNameSelfManagedRHOAI {
		t.Fatalf("DistributionNameFromPlatform(SelfManagedRhoai) = %q", got)
	}

	if got := DistributionNameFromPlatform("unknown-platform"); got != "" {
		t.Fatalf("DistributionNameFromPlatform(unknown) = %q, want empty", got)
	}
}

func TestTruncateForLog(t *testing.T) {
	t.Parallel()

	if got := truncateForLog("short", 128); got != "short" {
		t.Fatalf("truncateForLog(short) = %q", got)
	}

	long := strings.Repeat("a", maxLoggedPlatformLength+10)
	got := truncateForLog(long, maxLoggedPlatformLength)
	want := strings.Repeat("a", maxLoggedPlatformLength) + "..."
	if got != want {
		t.Fatalf("truncateForLog(long) = %q, want truncated", got)
	}
}

func TestValidateDistributionValueRuneLength(t *testing.T) {
	t.Parallel()

	// 64 multi-byte runes should be accepted (CRD MaxLength is character-based).
	ok := strings.Repeat("é", maxDistributionFieldLength)
	if err := validateDistributionVersion(ok); err != nil {
		t.Fatalf("validateDistributionVersion(64 runes) error = %v", err)
	}

	tooLong := strings.Repeat("é", maxDistributionFieldLength+1)
	if err := validateDistributionVersion(tooLong); err == nil {
		t.Fatal("validateDistributionVersion(65 runes) error = nil, want max length error")
	}
}

func TestReadDesiredDistributionInvalid(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	tests := []struct {
		name string
		data map[string]string
	}{
		{
			name: "unsupported distribution name",
			data: map[string]string{
				DistributionNameKey:    "NotARealDistribution",
				DistributionVersionKey: "1.0.0",
			},
		},
		{
			name: "control characters in version",
			data: map[string]string{
				DistributionNameKey:    platform.OpenDataHub,
				DistributionVersionKey: "1.0.0\nmalicious",
			},
		},
		{
			name: "oversized version",
			data: map[string]string{
				DistributionNameKey:    platform.OpenDataHub,
				DistributionVersionKey: strings.Repeat("a", maxDistributionFieldLength+1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ConfigMapName,
					Namespace: "opendatahub",
				},
				Data: tt.data,
			}
			cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

			_, err := ReadDesiredDistribution(context.Background(), cli, "opendatahub")
			if err == nil {
				t.Fatal("ReadDesiredDistribution() error = nil, want validation error")
			}
		})
	}
}

func TestReadDesiredDistributionEmptyNamespace(t *testing.T) {
	t.Parallel()

	got, err := ReadDesiredDistribution(context.Background(), fake.NewClientBuilder().Build(), "")
	if err != nil {
		t.Fatalf("ReadDesiredDistribution() error = %v", err)
	}

	if !IsDistributionEmpty(got) {
		t.Fatalf("ReadDesiredDistribution() = %#v, want empty", got)
	}
}

func TestReadPlatformVersionEmptyNamespace(t *testing.T) {
	t.Parallel()

	got, err := ReadPlatformVersion(context.Background(), fake.NewClientBuilder().Build(), "")
	if err != nil {
		t.Fatalf("ReadPlatformVersion() error = %v", err)
	}

	if got != "" {
		t.Fatalf("ReadPlatformVersion() = %q, want empty", got)
	}
}

func TestGetPlatformRelease(t *testing.T) {
	t.Parallel()

	releases := []componentsv1alpha1.ComponentRelease{
		{Name: "component-a", Version: "1.0.0"},
		{Name: ReleaseName, Version: "2.20.0"},
	}

	got := GetPlatformRelease(releases)
	if got.Name != ReleaseName || got.Version != "2.20.0" {
		t.Fatalf("GetPlatformRelease() = %#v", got)
	}

	if release := GetPlatformRelease(nil); release.Name != "" || release.Version != "" {
		t.Fatalf("GetPlatformRelease(nil) = %#v, want empty", release)
	}
}

func TestSetPlatformRelease(t *testing.T) {
	t.Parallel()

	releases := []componentsv1alpha1.ComponentRelease{
		{Name: "component-a", Version: "1.0.0"},
	}
	SetPlatformRelease(&releases, "2.20.0")

	if len(releases) != 2 || releases[1].Name != ReleaseName || releases[1].Version != "2.20.0" {
		t.Fatalf("SetPlatformRelease() append = %#v", releases)
	}

	SetPlatformRelease(&releases, "2.21.0")
	if releases[1].Version != "2.21.0" {
		t.Fatalf("SetPlatformRelease() replace = %#v", releases)
	}

	unchanged := []componentsv1alpha1.ComponentRelease{{Name: "component-a", Version: "1.0.0"}}
	SetPlatformRelease(&unchanged, "  ")
	if len(unchanged) != 1 {
		t.Fatalf("SetPlatformRelease() empty version = %#v", unchanged)
	}
}

func TestMergeComponentReleases(t *testing.T) {
	t.Parallel()

	componentReleases := []componentsv1alpha1.ComponentRelease{
		{Name: "component-a", Version: "1.0.0"},
		{Name: ReleaseName, Version: "stale"},
	}
	platformRelease := componentsv1alpha1.ComponentRelease{Name: ReleaseName, Version: "2.20.0"}

	got := MergeComponentReleases(componentReleases, platformRelease)
	if len(got) != 2 || got[0].Name != "component-a" || got[1].Version != "2.20.0" {
		t.Fatalf("MergeComponentReleases() = %#v", got)
	}

	if merged := MergeComponentReleases(componentReleases, componentsv1alpha1.ComponentRelease{}); len(merged) != 1 {
		t.Fatalf("MergeComponentReleases() without platform = %#v", merged)
	}
}

func TestReadPlatformVersion(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: "opendatahub",
		},
		Data: map[string]string{
			VersionDataKey: " 2.20.0 ",
		},
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()

	got, err := ReadPlatformVersion(context.Background(), cli, "opendatahub")
	if err != nil {
		t.Fatalf("ReadPlatformVersion() error = %v", err)
	}

	if got != "2.20.0" {
		t.Fatalf("ReadPlatformVersion() = %q, want %q", got, "2.20.0")
	}
}

func TestHandshakeComplete(t *testing.T) {
	t.Parallel()

	releases := []componentsv1alpha1.ComponentRelease{
		{Name: ReleaseName, Version: "2.20.0"},
	}

	if !HandshakeComplete("2.20.0", releases) {
		t.Fatal("HandshakeComplete() = false, want true")
	}

	if HandshakeComplete("2.21.0", releases) {
		t.Fatal("HandshakeComplete() = true, want false for version mismatch")
	}
}

func TestHandshakeRequired(t *testing.T) {
	t.Parallel()

	if HandshakeRequired(StandaloneDistribution("1.0.0")) {
		t.Fatal("HandshakeRequired() = true, want false for standalone")
	}

	if !HandshakeRequired(componentsv1alpha1.Distribution{Name: "OpenDataHub", Version: "3.5.1"}) {
		t.Fatal("HandshakeRequired() = false, want true for managed distribution")
	}
}
