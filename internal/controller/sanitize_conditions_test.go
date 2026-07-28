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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSanitizeConditions(t *testing.T) {
	tests := []struct {
		name     string
		input    []metav1.Condition
		expected []string // expected Reason for each condition
	}{
		{
			name:     "nil slice",
			input:    nil,
			expected: nil,
		},
		{
			name:     "empty slice",
			input:    []metav1.Condition{},
			expected: []string{},
		},
		{
			name: "all reasons populated",
			input: []metav1.Condition{
				{Type: "Ready", Reason: "ReconcileSuccess"},
				{Type: "Degraded", Reason: "NotDegraded"},
			},
			expected: []string{"ReconcileSuccess", "NotDegraded"},
		},
		{
			name: "some reasons empty",
			input: []metav1.Condition{
				{Type: "Ready", Reason: "ReconcileSuccess"},
				{Type: "ProvisioningSucceeded", Reason: ""},
				{Type: "Degraded", Reason: ""},
			},
			expected: []string{"ReconcileSuccess", conditionReasonUnknown, conditionReasonUnknown},
		},
		{
			name: "all reasons empty",
			input: []metav1.Condition{
				{Type: "Ready", Reason: ""},
				{Type: "Degraded", Reason: ""},
				{Type: "DeploymentsAvailable", Reason: ""},
			},
			expected: []string{conditionReasonUnknown, conditionReasonUnknown, conditionReasonUnknown},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sanitizeConditions(tc.input)

			if tc.expected == nil {
				return
			}

			if len(tc.input) != len(tc.expected) {
				t.Fatalf("expected %d conditions, got %d", len(tc.expected), len(tc.input))
			}

			for i, cond := range tc.input {
				if cond.Reason != tc.expected[i] {
					t.Errorf("condition[%d] (%s): expected Reason %q, got %q",
						i, cond.Type, tc.expected[i], cond.Reason)
				}
			}
		})
	}
}
