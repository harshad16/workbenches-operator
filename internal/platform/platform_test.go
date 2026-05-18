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

package platform_test

import (
	"testing"

	"github.com/opendatahub-io/workbenches-operator/internal/platform"
)

func TestSectionTitle(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     string
	}{
		{name: "ODH", platform: platform.OpenDataHub, want: "OpenShift Open Data Hub"},
		{name: "SelfManaged", platform: platform.SelfManagedRhoai, want: "OpenShift Self Managed Services"},
		{name: "unknown defaults to ODH", platform: "unknown", want: "OpenShift Open Data Hub"},
		{name: "empty defaults to ODH", platform: "", want: "OpenShift Open Data Hub"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := platform.SectionTitle(tt.platform)
			if got != tt.want {
				t.Errorf("SectionTitle(%q) = %q, want %q", tt.platform, got, tt.want)
			}
		})
	}
}

func TestDefaultNotebooksNamespace(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		want     string
	}{
		{name: "ODH", platform: platform.OpenDataHub, want: platform.DefaultNotebooksNamespaceODH},
		{name: "SelfManaged", platform: platform.SelfManagedRhoai, want: platform.DefaultNotebooksNamespaceRHOAI},
		{name: "unknown defaults to ODH", platform: "unknown", want: platform.DefaultNotebooksNamespaceODH},
		{name: "empty defaults to ODH", platform: "", want: platform.DefaultNotebooksNamespaceODH},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := platform.DefaultNotebooksNamespace(tt.platform)
			if got != tt.want {
				t.Errorf("DefaultNotebooksNamespace(%q) = %q, want %q", tt.platform, got, tt.want)
			}
		})
	}
}
