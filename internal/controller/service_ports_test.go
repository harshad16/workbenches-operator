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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
)

func TestSSAServicePortsDuplicateNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		live    []map[string]any
		desired []map[string]any
		wantDup bool
	}{
		{
			name: "metrics port number change keeps duplicate name",
			live: []map[string]any{
				{"name": "metrics", "port": int64(8080), "targetPort": "metrics"},
			},
			desired: []map[string]any{
				{"name": "metrics", "port": int64(8443), "targetPort": "metrics"},
			},
			wantDup: true,
		},
		{
			name: "same port number overwrites name in place",
			live: []map[string]any{
				{"name": "https", "port": int64(8443)},
			},
			desired: []map[string]any{
				{"name": "metrics", "port": int64(8443)},
			},
			wantDup: false,
		},
		{
			name: "identical ports are not a conflict",
			live: []map[string]any{
				{"name": "metrics", "port": int64(8443)},
			},
			desired: []map[string]any{
				{"name": "metrics", "port": float64(8443)},
			},
			wantDup: false,
		},
		{
			name: "default TCP matches explicit TCP",
			live: []map[string]any{
				{"name": "metrics", "port": int64(8080), "protocol": "TCP"},
			},
			desired: []map[string]any{
				{"name": "metrics", "port": int64(8443)},
			},
			wantDup: true,
		},
		{
			name: "extra uniquely named live port is kept alongside desired",
			live: []map[string]any{
				{"name": "webhook", "port": int64(443)},
			},
			desired: []map[string]any{
				{"name": "metrics", "port": int64(8443)},
			},
			wantDup: false,
		},
		{
			name: "no live ports",
			desired: []map[string]any{
				{"name": "metrics", "port": int64(8443)},
			},
			wantDup: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			live := serviceUnstructured(tt.live)
			desired := serviceUnstructured(tt.desired)

			got := ssaServicePortsDuplicateNames(live, desired)
			if got != tt.wantDup {
				t.Fatalf("ssaServicePortsDuplicateNames() = %v, want %v", got, tt.wantDup)
			}
		})
	}
}

func TestPrepareServiceForSSAReplacesConflictingPorts(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	live := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "odh-notebook-controller-service",
			Namespace:       "redhat-ods-applications",
			ResourceVersion: "1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name: "metrics",
				Port: 8080,
			}},
		},
	}

	desired := serviceUnstructured([]map[string]any{
		{"name": "metrics", "port": int64(8443), "targetPort": "metrics"},
	})

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(live).Build()
	reconciler := &WorkbenchesReconciler{Client: fakeClient, Scheme: scheme}

	if err := reconciler.prepareServiceForSSA(context.Background(), desired); err != nil {
		t.Fatalf("prepareServiceForSSA() error = %v", err)
	}

	updated := &corev1.Service{}
	if err := fakeClient.Get(context.Background(), client.ObjectKeyFromObject(live), updated); err != nil {
		t.Fatalf("Get updated Service: %v", err)
	}

	if len(updated.Spec.Ports) != 1 {
		t.Fatalf("ports len = %d, want 1: %+v", len(updated.Spec.Ports), updated.Spec.Ports)
	}

	if updated.Spec.Ports[0].Name != "metrics" || updated.Spec.Ports[0].Port != 8443 {
		t.Fatalf("updated port = %+v, want name=metrics port=8443", updated.Spec.Ports[0])
	}
}

func TestPrepareServiceForSSANoopsWithoutConflict(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	live := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "odh-notebook-controller-service",
			Namespace:       "redhat-ods-applications",
			ResourceVersion: "1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name: "metrics",
				Port: 8443,
			}},
		},
	}

	desired := serviceUnstructured([]map[string]any{
		{"name": "metrics", "port": int64(8443), "targetPort": "metrics"},
	})

	var patched bool

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(live).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, _ client.Patch, _ ...client.PatchOption) error {
				patched = true

				return nil
			},
		}).
		Build()

	reconciler := &WorkbenchesReconciler{Client: fakeClient, Scheme: scheme}

	if err := reconciler.prepareServiceForSSA(context.Background(), desired); err != nil {
		t.Fatalf("prepareServiceForSSA() error = %v", err)
	}

	if patched {
		t.Fatal("expected no patch when Service ports do not conflict")
	}
}

func TestApplyObjectsRewritesConflictingServicePorts(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(componentsv1alpha1.AddToScheme(scheme))

	owner := &componentsv1alpha1.Workbenches{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentsv1alpha1.WorkbenchesInstanceName,
			UID:  "owner-uid-service-ports",
		},
	}

	live := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "odh-notebook-controller-service",
			Namespace:       "redhat-ods-applications",
			ResourceVersion: "1",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Name: "metrics",
				Port: 8080,
			}},
		},
	}

	desired := serviceUnstructured([]map[string]any{
		{"name": "metrics", "port": int64(8443), "targetPort": "metrics"},
	})
	_ = unstructured.SetNestedStringMap(desired.Object, map[string]string{"app": "odh-notebook-controller"}, "spec", "selector")

	var patchTypes []types.PatchType

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(live).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(_ context.Context, _ client.WithWatch, _ client.Object, patch client.Patch, _ ...client.PatchOption) error {
				patchTypes = append(patchTypes, patch.Type())

				return nil
			},
		}).
		Build()

	reconciler := &WorkbenchesReconciler{Client: fakeClient, Scheme: scheme}

	if err := reconciler.applyObjects(context.Background(), owner, []*unstructured.Unstructured{desired}); err != nil {
		t.Fatalf("applyObjects() error = %v", err)
	}

	if len(patchTypes) != 2 {
		t.Fatalf("patch calls = %d (%v), want 2 (merge then apply)", len(patchTypes), patchTypes)
	}

	if patchTypes[0] != types.MergePatchType {
		t.Errorf("first patch type = %s, want %s", patchTypes[0], types.MergePatchType)
	}

	if patchTypes[1] != types.ApplyPatchType {
		t.Errorf("second patch type = %s, want %s", patchTypes[1], types.ApplyPatchType)
	}
}

func TestPrepareServiceForSSASkipsNonServices(t *testing.T) {
	t.Parallel()

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      "manager",
			"namespace": "redhat-ods-applications",
		},
	}}

	reconciler := &WorkbenchesReconciler{}
	if err := reconciler.prepareServiceForSSA(context.Background(), obj); err != nil {
		t.Fatalf("prepareServiceForSSA() error = %v", err)
	}
}

func serviceUnstructured(ports []map[string]any) *unstructured.Unstructured {
	portAny := make([]any, len(ports))
	for i, port := range ports {
		portAny[i] = port
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      "odh-notebook-controller-service",
			"namespace": "redhat-ods-applications",
		},
		"spec": map[string]any{
			"ports": portAny,
		},
	}}
}
