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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
)

func containerResources(obj *unstructured.Unstructured) (map[string]any, map[string]any) {
	containers, _, _ := unstructured.NestedSlice(obj.Object, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return nil, nil
	}

	c0, _ := containers[0].(map[string]any)
	resources, _ := c0["resources"].(map[string]any)
	if resources == nil {
		return nil, nil
	}

	requests, _ := resources["requests"].(map[string]any)
	limits, _ := resources["limits"].(map[string]any)

	return requests, limits
}

func TestMergeDeploymentsOverride(t *testing.T) {
	t.Parallel()

	source, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "manager",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1001m"),
								corev1.ResourceMemory: resource.MustParse("3Gi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("1001m"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("source ToUnstructured: %v", err)
	}

	target, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](3),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "manager",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("500m"),
								corev1.ResourceMemory: resource.MustParse("4Gi"),
							},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("target ToUnstructured: %v", err)
	}

	src := unstructured.Unstructured{Object: source}
	trg := unstructured.Unstructured{Object: target}

	if mergeErr := mergeDeployments(&src, &trg); mergeErr != nil {
		t.Fatalf("mergeDeployments: %v", mergeErr)
	}

	replicas, found, nestErr := unstructured.NestedInt64(trg.Object, "spec", "replicas")
	if nestErr != nil || !found {
		t.Fatalf("replicas found=%v err=%v", found, nestErr)
	}

	if replicas != 1 {
		t.Errorf("replicas = %d, want 1 (live value)", replicas)
	}

	requests, limits := containerResources(&trg)
	if requests["cpu"] != "1001m" {
		t.Errorf("requests.cpu = %v, want 1001m", requests["cpu"])
	}

	if requests["memory"] != "3Gi" {
		t.Errorf("requests.memory = %v, want 3Gi", requests["memory"])
	}

	if limits["cpu"] != "1001m" {
		t.Errorf("limits.cpu = %v, want 1001m", limits["cpu"])
	}

	if limits["memory"] != "4Gi" {
		t.Errorf("limits.memory = %v, want 4Gi", limits["memory"])
	}
}

func TestMergeDeploymentsRemove(t *testing.T) {
	t.Parallel()

	source, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "manager",
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("source ToUnstructured: %v", err)
	}

	target, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](3),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name: "manager",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("500m"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse("500m"),
							},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("target ToUnstructured: %v", err)
	}

	src := unstructured.Unstructured{Object: source}
	trg := unstructured.Unstructured{Object: target}

	if mergeErr := mergeDeployments(&src, &trg); mergeErr != nil {
		t.Fatalf("mergeDeployments: %v", mergeErr)
	}

	_, found, nestErr := unstructured.NestedFieldNoCopy(trg.Object, "spec", "replicas")
	if nestErr != nil {
		t.Fatalf("replicas lookup: %v", nestErr)
	}

	if found {
		t.Error("expected replicas to be removed when absent on live Deployment")
	}

	containers, _, sliceErr := unstructured.NestedSlice(trg.Object, "spec", "template", "spec", "containers")
	if sliceErr != nil {
		t.Fatalf("containers: %v", sliceErr)
	}

	c0, _ := containers[0].(map[string]any)
	if _, hasResources := c0["resources"]; hasResources {
		t.Error("expected resources to be removed when absent on live container")
	}
}

func TestMergeDeploymentsMalformedContainersPath(t *testing.T) {
	t.Parallel()

	source := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": "not-a-slice",
				},
			},
		},
	}}
	target := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"replicas": int64(1),
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "manager"},
					},
				},
			},
		},
	}}

	if err := mergeDeployments(source, target); err == nil {
		t.Fatal("expected error for malformed source containers path")
	}

	sourceOK := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": []any{
						map[string]any{"name": "manager"},
					},
				},
			},
		},
	}}
	targetBad := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"spec": map[string]any{
					"containers": map[string]any{"oops": true},
				},
			},
		},
	}}

	if err := mergeDeployments(sourceOK, targetBad); err == nil {
		t.Fatal("expected error for malformed target containers path")
	}
}
