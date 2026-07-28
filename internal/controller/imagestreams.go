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
	"fmt"
	"strings"

	imagev1 "github.com/openshift/api/image/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/metadata"
)

const (
	// maxImageStreamConditionMessageLen caps per-tag error text (matches ODH).
	maxImageStreamConditionMessageLen = 100
	// maxFailedImageStreamTags caps how many failed tags appear in the condition message.
	maxFailedImageStreamTags = 10
)

// syncImageStreamsAvailable updates Workbenches status.conditions ImageStreamsAvailable
// to mirror in-tree ODH imagestreams.NewAction. Informational only — does not gate Ready.
// When the ImageStream API is missing (vanilla Kubernetes), the condition is left unset.
func (r *WorkbenchesReconciler) syncImageStreamsAvailable(
	ctx context.Context,
	wb *componentsv1alpha1.Workbenches,
	namespace string,
) error {
	imageStreams := &imagev1.ImageStreamList{}

	err := r.List(ctx, imageStreams,
		client.InNamespace(namespace),
		client.MatchingLabels{
			metadata.PartOfLabelKey: metadata.ComponentLabelValue,
		},
	)
	if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
		// ImageStream API missing (vanilla K8s / envtest) or types not in scheme.
		return nil
	}

	if err != nil {
		return fmt.Errorf("error listing ImageStreams: %w", err)
	}

	failedTags := failedImageStreamTags(imageStreams.Items)
	if len(failedTags) == 0 {
		meta.SetStatusCondition(&wb.Status.Conditions, metav1.Condition{
			Type:               conditionTypeImageStreamsAvailable,
			Status:             metav1.ConditionTrue,
			Reason:             conditionReasonAvailable,
			Message:            "All ImageStream tags imported successfully",
			ObservedGeneration: wb.Generation,
		})

		return nil
	}

	reported := failedTags
	suffix := ""

	if len(reported) > maxFailedImageStreamTags {
		suffix = fmt.Sprintf("; ... and %d more", len(reported)-maxFailedImageStreamTags)
		reported = reported[:maxFailedImageStreamTags]
	}

	meta.SetStatusCondition(&wb.Status.Conditions, metav1.Condition{
		Type:   conditionTypeImageStreamsAvailable,
		Status: metav1.ConditionFalse,
		Reason: conditionReasonImageStreamsNotReady,
		Message: fmt.Sprintf(
			"Warning: %d ImageStream tag(s) failed to import: %s%s",
			len(failedTags),
			strings.Join(reported, "; "),
			suffix,
		),
		ObservedGeneration: wb.Generation,
	})

	return nil
}

func failedImageStreamTags(streams []imagev1.ImageStream) []string {
	var failedTags []string

	for i := range streams {
		is := &streams[i]
		for _, tagStatus := range is.Status.Tags {
			if len(tagStatus.Items) > 0 {
				continue
			}

			for _, cond := range tagStatus.Conditions {
				if cond.Type != imagev1.ImportSuccess || cond.Status != corev1.ConditionFalse {
					continue
				}

				msg := cond.Message
				if len(msg) > maxImageStreamConditionMessageLen {
					msg = msg[:maxImageStreamConditionMessageLen] + "..."
				}

				failedTags = append(failedTags, fmt.Sprintf("%s:%s (%s)", is.Name, tagStatus.Tag, msg))
			}
		}
	}

	return failedTags
}

// appendImageStreamWarningToReady mirrors ODH DSC aggregation: when
// ImageStreamsAvailable is False, append its message to Ready without changing Ready status.
func appendImageStreamWarningToReady(wb *componentsv1alpha1.Workbenches) {
	isCond := meta.FindStatusCondition(wb.Status.Conditions, conditionTypeImageStreamsAvailable)
	if isCond == nil || isCond.Status != metav1.ConditionFalse || isCond.Message == "" {
		return
	}

	readyCond := meta.FindStatusCondition(wb.Status.Conditions, conditionTypeReady)
	if readyCond == nil {
		return
	}

	msg := readyCond.Message
	if msg != "" {
		msg = strings.TrimRight(msg, ".") + ". " + isCond.Message
	} else {
		msg = isCond.Message
	}

	meta.SetStatusCondition(&wb.Status.Conditions, metav1.Condition{
		Type:               readyCond.Type,
		Status:             readyCond.Status,
		Reason:             readyCond.Reason,
		Message:            msg,
		ObservedGeneration: readyCond.ObservedGeneration,
	})
}
