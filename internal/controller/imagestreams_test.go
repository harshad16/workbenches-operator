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
	"context"
	"strings"
	"testing"

	imagev1 "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/metadata"
)

func TestFailedImageStreamTags(t *testing.T) {
	t.Parallel()

	healthy := imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: "healthy"},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{{
				Tag:   "latest",
				Items: []imagev1.TagEvent{{Image: "sha256:abc"}},
			}},
		},
	}

	failed := imagev1.ImageStream{
		ObjectMeta: metav1.ObjectMeta{Name: "broken"},
		Status: imagev1.ImageStreamStatus{
			Tags: []imagev1.NamedTagEventList{{
				Tag: "cuda",
				Conditions: []imagev1.TagEventCondition{{
					Type:    imagev1.ImportSuccess,
					Status:  corev1.ConditionFalse,
					Message: "manifest unknown",
				}},
			}},
		},
	}

	if got := failedImageStreamTags([]imagev1.ImageStream{healthy}); len(got) != 0 {
		t.Fatalf("healthy tags: got %v, want none", got)
	}

	got := failedImageStreamTags([]imagev1.ImageStream{failed})
	if len(got) != 1 || !strings.Contains(got[0], "broken:cuda") {
		t.Fatalf("failed tags = %v, want broken:cuda entry", got)
	}
}

func TestSyncImageStreamsAvailable(t *testing.T) {
	t.Parallel()

	namespace := "opendatahub"
	partOfLabels := map[string]string{metadata.PartOfLabelKey: metadata.ComponentLabelValue}

	tests := []struct {
		name           string
		withImageAPI   bool
		objects        []runtime.Object
		wantCond       bool
		wantStatus     metav1.ConditionStatus
		wantReason     string
		wantMsgContain string
	}{
		{
			name:         "failed import sets False",
			withImageAPI: true,
			objects: []runtime.Object{
				&imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "jupyter-cuda",
						Namespace: namespace,
						Labels:    partOfLabels,
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{
							Tag: "latest",
							Conditions: []imagev1.TagEventCondition{{
								Type:    imagev1.ImportSuccess,
								Status:  corev1.ConditionFalse,
								Message: "import failed",
							}},
						}},
					},
				},
			},
			wantCond:       true,
			wantStatus:     metav1.ConditionFalse,
			wantReason:     conditionReasonImageStreamsNotReady,
			wantMsgContain: "jupyter-cuda:latest",
		},
		{
			name:         "healthy tags set True",
			withImageAPI: true,
			objects: []runtime.Object{
				&imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "jupyter-minimal",
						Namespace: namespace,
						Labels:    partOfLabels,
					},
					Status: imagev1.ImageStreamStatus{
						Tags: []imagev1.NamedTagEventList{{
							Tag:   "latest",
							Items: []imagev1.TagEvent{{Image: "sha256:abc"}},
						}},
					},
				},
			},
			wantCond:   true,
			wantStatus: metav1.ConditionTrue,
			wantReason: conditionReasonAvailable,
		},
		{
			name:         "no managed ImageStreams sets True",
			withImageAPI: true,
			objects:      nil,
			wantCond:     true,
			wantStatus:   metav1.ConditionTrue,
			wantReason:   conditionReasonAvailable,
		},
		{
			name:         "ImageStream API unavailable leaves condition unset",
			withImageAPI: false,
			objects:      nil,
			wantCond:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			utilruntime.Must(componentsv1alpha1.AddToScheme(scheme))
			if tt.withImageAPI {
				utilruntime.Must(imagev1.Install(scheme))
			}

			wb := &componentsv1alpha1.Workbenches{
				ObjectMeta: metav1.ObjectMeta{Name: componentsv1alpha1.WorkbenchesInstanceName, Generation: 3},
			}

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tt.objects) > 0 {
				builder = builder.WithRuntimeObjects(tt.objects...)
			}
			r := &WorkbenchesReconciler{Client: builder.Build(), Scheme: scheme}

			if err := r.syncImageStreamsAvailable(context.Background(), wb, namespace); err != nil {
				t.Fatalf("syncImageStreamsAvailable() error = %v", err)
			}

			cond := meta.FindStatusCondition(wb.Status.Conditions, conditionTypeImageStreamsAvailable)
			if !tt.wantCond {
				if cond != nil {
					t.Fatalf("expected no ImageStreamsAvailable condition, got %+v", cond)
				}

				return
			}

			if cond == nil {
				t.Fatal("expected ImageStreamsAvailable condition")
			}
			if cond.Status != tt.wantStatus {
				t.Fatalf("Status = %s, want %s", cond.Status, tt.wantStatus)
			}
			if tt.wantReason != "" && cond.Reason != tt.wantReason {
				t.Fatalf("Reason = %s, want %s", cond.Reason, tt.wantReason)
			}
			if tt.wantMsgContain != "" && !strings.Contains(cond.Message, tt.wantMsgContain) {
				t.Fatalf("Message = %q, want substring %q", cond.Message, tt.wantMsgContain)
			}
		})
	}
}

func TestAppendImageStreamWarningToReady(t *testing.T) {
	t.Parallel()

	wb := &componentsv1alpha1.Workbenches{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
	}
	meta.SetStatusCondition(&wb.Status.Conditions, metav1.Condition{
		Type:    conditionTypeReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ReconcileSuccess",
		Message: "Workbenches component is ready",
	})
	meta.SetStatusCondition(&wb.Status.Conditions, metav1.Condition{
		Type:    conditionTypeImageStreamsAvailable,
		Status:  metav1.ConditionFalse,
		Reason:  conditionReasonImageStreamsNotReady,
		Message: "Warning: 1 ImageStream tag(s) failed to import: jupyter-cuda:latest (import failed)",
	})

	appendImageStreamWarningToReady(wb)

	ready := meta.FindStatusCondition(wb.Status.Conditions, conditionTypeReady)
	if ready == nil || ready.Status != metav1.ConditionTrue {
		t.Fatal("Ready status must stay True")
	}
	if !strings.Contains(ready.Message, "Warning: 1 ImageStream tag(s) failed") {
		t.Fatalf("Ready message = %q, want ImageStream warning appended", ready.Message)
	}
}

func TestIsManagedPartOfLabel(t *testing.T) {
	t.Parallel()

	managed := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{metadata.PartOfLabelKey: metadata.ComponentLabelValue},
		},
	}
	if !isManagedPartOfLabel(managed) {
		t.Fatal("expected managed label to match")
	}
	if isManagedPartOfLabel(&corev1.ConfigMap{}) {
		t.Fatal("expected unlabeled object to be filtered out")
	}
	if isManagedPartOfLabel(nil) {
		t.Fatal("expected nil to be filtered out")
	}
}
