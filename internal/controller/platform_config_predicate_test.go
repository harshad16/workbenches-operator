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

package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/opendatahub-io/workbenches-operator/internal/platformconfig"
)

func TestPlatformConfigChangedPredicate(t *testing.T) {
	t.Parallel()

	predicate := newPlatformConfigChangedPredicate("opendatahub")

	tests := []struct {
		name string
		old  *corev1.ConfigMap
		new  *corev1.ConfigMap
		want bool
	}{
		{
			name: "platform version changed",
			old:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0"),
			new:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.21.0"),
			want: true,
		},
		{
			name: "distribution version changed",
			old:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0"),
			new:  platformConfigMap("opendatahub", "OpenDataHub", "3.6.0", "2.20.0"),
			want: true,
		},
		{
			name: "unrelated configmap",
			old:  platformConfigMap("other-ns", "OpenDataHub", "3.5.0", "2.20.0"),
			new:  platformConfigMap("other-ns", "OpenDataHub", "3.6.0", "2.21.0"),
			want: false,
		},
		{
			name: "distribution name changed",
			old:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0"),
			new:  platformConfigMap("opendatahub", platformconfig.DistributionNameSelfManagedRHOAI, "3.5.0", "2.20.0"),
			want: true,
		},
		{
			name: "unchanged platform config",
			old:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0"),
			new:  platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := predicate.Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new})
			if got != tt.want {
				t.Fatalf("Update() = %v, want %v", got, tt.want)
			}
		})
	}

	if !predicate.Create(event.CreateEvent{Object: platformConfigMap("opendatahub", "OpenDataHub", "3.5.0", "2.20.0")}) {
		t.Fatal("Create() = false, want true")
	}
}

func platformConfigMap(namespace, distributionName, distributionVersion, platformVersion string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      platformconfig.ConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			platformconfig.DistributionNameKey:    distributionName,
			platformconfig.DistributionVersionKey: distributionVersion,
			platformconfig.VersionDataKey:         platformVersion,
		},
	}
}
