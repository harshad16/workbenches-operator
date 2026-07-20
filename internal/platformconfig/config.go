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

// Package platformconfig reads platform-managed configuration for module controllers.
package platformconfig

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/platform"
)

const (
	// ConfigMapName is the platform-managed ConfigMap for the workbenches module.
	ConfigMapName = "odh-workbenches-config"

	// DistributionNameKey is the ConfigMap data key for the desired distribution name.
	DistributionNameKey = "distribution.name"

	// DistributionVersionKey is the ConfigMap data key for the desired distribution version.
	DistributionVersionKey = "distribution.version"

	// VersionDataKey is the ConfigMap data key for the platform version handshake.
	VersionDataKey = "platformVersion"

	// ReleaseName is the status.releases entry name for the platform version handshake.
	ReleaseName = "platform"

	// DistributionNameStandalone is reported in status when no platform ConfigMap is present.
	DistributionNameStandalone = "Standalone"

	// DistributionNameSelfManagedRHOAI is the platform distribution name for RHOAI.
	// Note: this differs from platform.SelfManagedRhoai ("SelfManagedRhoai") which is the
	// projected spec.platform value; distribution status uses the RHOAI product spelling.
	DistributionNameSelfManagedRHOAI = "SelfManagedRHOAI"

	// maxDistributionFieldLength matches the CRD MaxLength on status.distribution fields.
	maxDistributionFieldLength = 64

	// maxLoggedPlatformLength bounds untrusted spec.platform values written to logs.
	maxLoggedPlatformLength = 128
)

// ReadDesiredDistribution returns distribution.name and distribution.version from
// odh-workbenches-config. A missing ConfigMap yields an empty distribution without error.
func ReadDesiredDistribution(
	ctx context.Context,
	c client.Reader,
	namespace string,
) (componentsv1alpha1.Distribution, error) {
	if namespace == "" {
		return componentsv1alpha1.Distribution{}, nil
	}

	cm := &corev1.ConfigMap{}

	err := c.Get(ctx, client.ObjectKey{Name: ConfigMapName, Namespace: namespace}, cm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return componentsv1alpha1.Distribution{}, nil
		}

		return componentsv1alpha1.Distribution{}, fmt.Errorf("reading ConfigMap %s/%s: %w", namespace, ConfigMapName, err)
	}

	if cm.Data == nil {
		return componentsv1alpha1.Distribution{}, nil
	}

	name := strings.TrimSpace(cm.Data[DistributionNameKey])
	version := strings.TrimSpace(cm.Data[DistributionVersionKey])
	if err := validateDistributionFields(name, version); err != nil {
		return componentsv1alpha1.Distribution{}, fmt.Errorf("invalid distribution in ConfigMap %s/%s: %w", namespace, ConfigMapName, err)
	}

	return componentsv1alpha1.Distribution{
		Name:    name,
		Version: version,
	}, nil
}

// ReadPlatformVersion returns data.platformVersion from odh-workbenches-config.
// A missing ConfigMap or key yields an empty string without error.
func ReadPlatformVersion(ctx context.Context, c client.Reader, namespace string) (string, error) {
	if namespace == "" {
		return "", nil
	}

	cm := &corev1.ConfigMap{}

	err := c.Get(ctx, client.ObjectKey{Name: ConfigMapName, Namespace: namespace}, cm)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}

		return "", fmt.Errorf("reading ConfigMap %s/%s: %w", namespace, ConfigMapName, err)
	}

	if cm.Data == nil {
		return "", nil
	}

	return strings.TrimSpace(cm.Data[VersionDataKey]), nil
}

// DistributionAligned reports whether the module has reconciled against the desired distribution.
func DistributionAligned(desired, current componentsv1alpha1.Distribution) bool {
	if desired.Name == "" {
		return false
	}

	return desired.Name == current.Name && desired.Version == current.Version
}

// StandaloneDistribution returns the distribution status for clusters without platform management.
func StandaloneDistribution(operatorVersion string) componentsv1alpha1.Distribution {
	version := strings.TrimSpace(operatorVersion)
	if version == "" || validateDistributionVersion(version) != nil {
		version = "0.0.0"
	}

	return componentsv1alpha1.Distribution{
		Name:    DistributionNameStandalone,
		Version: version,
	}
}

// IsDistributionEmpty reports whether both distribution fields are unset.
func IsDistributionEmpty(d componentsv1alpha1.Distribution) bool {
	return strings.TrimSpace(d.Name) == "" && strings.TrimSpace(d.Version) == ""
}

// ResolveDesiredDistribution applies standalone and spec.platform fallbacks when the ConfigMap
// does not provide a complete desired distribution.
func ResolveDesiredDistribution(
	desired componentsv1alpha1.Distribution,
	specPlatform string,
	operatorVersion string,
) componentsv1alpha1.Distribution {
	if IsDistributionEmpty(desired) {
		return StandaloneDistribution(operatorVersion)
	}

	if desired.Name == "" && specPlatform != "" {
		// Prefer ConfigMap values; only derive the name from spec.platform when distribution.name is absent.
		desired.Name = DistributionNameFromPlatform(specPlatform)
		if desired.Name == "" {
			log.Log.Info("unrecognized spec.platform; ignoring for distribution name mapping",
				"platform", truncateForLog(specPlatform, maxLoggedPlatformLength))
		}
	}

	if desired.Name == "" {
		desired.Name = StandaloneDistribution(operatorVersion).Name
	}

	if desired.Version == "" {
		desired.Version = StandaloneDistribution(operatorVersion).Version
	}

	return desired
}

// DistributionNameFromPlatform maps projected spec.platform values to distribution names.
func DistributionNameFromPlatform(specPlatform string) string {
	switch specPlatform {
	case platform.SelfManagedRhoai:
		return DistributionNameSelfManagedRHOAI
	case platform.OpenDataHub:
		return platform.OpenDataHub
	default:
		return ""
	}
}

func validateDistributionFields(name, version string) error {
	if name != "" {
		switch name {
		case platform.OpenDataHub, DistributionNameSelfManagedRHOAI, DistributionNameStandalone:
		default:
			return fmt.Errorf("unsupported distribution.name %q", name)
		}
	}

	if err := validateDistributionVersion(version); err != nil {
		return err
	}

	return nil
}

func validateDistributionVersion(value string) error {
	if value == "" {
		return nil
	}

	if utf8.RuneCountInString(value) > maxDistributionFieldLength {
		return fmt.Errorf("%s exceeds max length %d", DistributionVersionKey, maxDistributionFieldLength)
	}

	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains control characters", DistributionVersionKey)
		}
	}

	return nil
}

func truncateForLog(value string, maxRunes int) string {
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}

	runes := []rune(value)
	return string(runes[:maxRunes]) + "..."
}

// GetPlatformRelease returns the platform handshake entry from status.releases.
func GetPlatformRelease(releases []componentsv1alpha1.ComponentRelease) componentsv1alpha1.ComponentRelease {
	for _, release := range releases {
		if release.Name == ReleaseName {
			return release
		}
	}

	return componentsv1alpha1.ComponentRelease{}
}

// SetPlatformRelease records the reconciled platform version in status.releases.
func SetPlatformRelease(releases *[]componentsv1alpha1.ComponentRelease, version string) {
	version = strings.TrimSpace(version)
	if version == "" {
		return
	}

	for i, release := range *releases {
		if release.Name == ReleaseName {
			(*releases)[i].Version = version

			return
		}
	}

	*releases = append(*releases, componentsv1alpha1.ComponentRelease{
		Name:    ReleaseName,
		Version: version,
	})
}

// MergeComponentReleases combines upstream component releases with the platform handshake entry.
func MergeComponentReleases(
	componentReleases []componentsv1alpha1.ComponentRelease,
	platformRelease componentsv1alpha1.ComponentRelease,
) []componentsv1alpha1.ComponentRelease {
	merged := make([]componentsv1alpha1.ComponentRelease, 0, len(componentReleases)+1)

	for _, release := range componentReleases {
		if release.Name == ReleaseName {
			continue
		}

		merged = append(merged, release)
	}

	if platformRelease.Name == ReleaseName && strings.TrimSpace(platformRelease.Version) != "" {
		merged = append(merged, platformRelease)
	}

	return merged
}

// HandshakeComplete reports whether the module has recorded the target platform version.
func HandshakeComplete(platformVersion string, releases []componentsv1alpha1.ComponentRelease) bool {
	platformVersion = strings.TrimSpace(platformVersion)
	if platformVersion == "" {
		return false
	}

	return GetPlatformRelease(releases).Version == platformVersion
}

// HandshakeRequired reports whether the platform version handshake gates Ready.
func HandshakeRequired(desired componentsv1alpha1.Distribution) bool {
	return desired.Name != DistributionNameStandalone
}
