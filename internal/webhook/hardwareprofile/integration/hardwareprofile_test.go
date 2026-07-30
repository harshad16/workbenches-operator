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

package integration_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/opendatahub-io/workbenches-operator/internal/gvk"
	"github.com/opendatahub-io/workbenches-operator/internal/metadata"
)

func newHardwareProfile(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": gvk.HardwareProfile.Group + "/" + gvk.HardwareProfile.Version,
			"kind":       gvk.HardwareProfile.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": testNamespace,
			},
			"spec": spec,
		},
	}
}

func newNotebook(name string, annotations map[string]string) *unstructured.Unstructured {
	nb := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": gvk.Notebook.Group + "/" + gvk.Notebook.Version,
			"kind":       gvk.Notebook.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": testNamespace,
			},
			"spec": map[string]any{
				"template": map[string]any{
					"spec": map[string]any{
						"containers": []any{
							map[string]any{
								"name":  name,
								"image": "quay.io/test/notebook:latest",
							},
						},
					},
				},
			},
		},
	}

	if annotations != nil {
		nb.SetAnnotations(annotations)
	}

	return nb
}

var _ = Describe("HardwareProfile Webhook Integration", func() {
	Context("Resource injection", func() {
		It("should inject CPU and memory resources from HardwareProfile into Notebook", func() {
			hwp := newHardwareProfile("cpu-medium", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "1",
						"defaultCount": "2",
						"resourceType": "CPU",
					},
					map[string]any{
						"displayName":  "Memory",
						"identifier":   "memory",
						"minCount":     "2Gi",
						"defaultCount": "4Gi",
						"resourceType": "Memory",
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-resource-inject", map[string]string{
				metadata.HardwareProfileNameAnnotation: "cpu-medium",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			// Fetch the created notebook and verify resources were injected
			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			containers, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).To(HaveLen(1))

			container, ok := containers[0].(map[string]any)
			Expect(ok).To(BeTrue())

			resources, found, err := unstructured.NestedMap(container, "resources")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue(), "resources should be injected")

			requests, found, err := unstructured.NestedMap(resources, "requests")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(requests).To(HaveKeyWithValue("cpu", "2"))
			Expect(requests).To(HaveKeyWithValue("memory", "4Gi"))

			limits, found, err := unstructured.NestedMap(resources, "limits")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(limits).To(HaveKeyWithValue("cpu", "2"))
			Expect(limits).To(HaveKeyWithValue("memory", "4Gi"))

			// Verify namespace annotation was set
			annotations := created.GetAnnotations()
			Expect(annotations).To(HaveKeyWithValue(metadata.HardwareProfileNamespaceAnnotation, testNamespace))
		})

		It("should inject GPU resources from HardwareProfile into Notebook", func() {
			hwp := newHardwareProfile("gpu-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "4",
						"defaultCount": "8",
						"resourceType": "CPU",
					},
					map[string]any{
						"displayName":  "Memory",
						"identifier":   "memory",
						"minCount":     "8Gi",
						"defaultCount": "16Gi",
						"resourceType": "Memory",
					},
					map[string]any{
						"displayName":  "GPU",
						"identifier":   "nvidia.com/gpu",
						"minCount":     "1",
						"defaultCount": "1",
						"resourceType": "Accelerator",
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-gpu-inject", map[string]string{
				metadata.HardwareProfileNameAnnotation: "gpu-profile",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			containers, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).NotTo(BeEmpty())

			container, ok := containers[0].(map[string]any)
			Expect(ok).To(BeTrue())

			limits, _, _ := unstructured.NestedMap(container, "resources", "limits")
			Expect(limits).To(HaveKeyWithValue("nvidia.com/gpu", "1"))
		})
	})

	Context("Kueue LocalQueue support", func() {
		It("should set Kueue queue-name label from HardwareProfile", func() {
			hwp := newHardwareProfile("kueue-profile", map[string]any{
				"scheduling": map[string]any{
					"kueue": map[string]any{
						"localQueueName": "team-a-queue",
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-kueue", map[string]string{
				metadata.HardwareProfileNameAnnotation: "kueue-profile",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			labels := created.GetLabels()
			Expect(labels).To(HaveKeyWithValue("kueue.x-k8s.io/queue-name", "team-a-queue"))
		})

		It("should skip nodeSelector/tolerations when Kueue is configured", func() {
			hwp := newHardwareProfile("kueue-with-scheduling", map[string]any{
				"scheduling": map[string]any{
					"kueue": map[string]any{
						"localQueueName": "gpu-queue",
					},
					"node": map[string]any{
						"nodeSelector": map[string]any{
							"gpu": "true",
						},
						"tolerations": []any{
							map[string]any{
								"key":      "nvidia.com/gpu",
								"operator": "Exists",
								"effect":   "NoSchedule",
							},
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-kueue-skip-sched", map[string]string{
				metadata.HardwareProfileNameAnnotation: "kueue-with-scheduling",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			// Kueue label should be set
			labels := created.GetLabels()
			Expect(labels).To(HaveKeyWithValue("kueue.x-k8s.io/queue-name", "gpu-queue"))

			// nodeSelector should NOT be set (Kueue takes priority)
			_, found, _ := unstructured.NestedMap(created.Object, "spec", "template", "spec", "nodeSelector")
			Expect(found).To(BeFalse(), "nodeSelector should not be applied when Kueue is configured")

			// tolerations should NOT be set
			_, found, _ = unstructured.NestedSlice(created.Object, "spec", "template", "spec", "tolerations")
			Expect(found).To(BeFalse(), "tolerations should not be applied when Kueue is configured")
		})
	})

	Context("Node scheduling", func() {
		It("should apply nodeSelector and tolerations from HardwareProfile", func() {
			hwp := newHardwareProfile("scheduled-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "1",
						"defaultCount": "2",
						"resourceType": "CPU",
					},
				},
				"scheduling": map[string]any{
					"node": map[string]any{
						"nodeSelector": map[string]any{
							"node-type": "compute",
						},
						"tolerations": []any{
							map[string]any{
								"key":      "dedicated",
								"operator": "Equal",
								"value":    "compute",
								"effect":   "NoSchedule",
							},
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-scheduling", map[string]string{
				metadata.HardwareProfileNameAnnotation: "scheduled-profile",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			nodeSelector, found, err := unstructured.NestedStringMap(created.Object, "spec", "template", "spec", "nodeSelector")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(nodeSelector).To(HaveKeyWithValue("node-type", "compute"))

			tolerations, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "tolerations")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(tolerations).To(HaveLen(1))

			tol, ok := tolerations[0].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(tol["key"]).To(Equal("dedicated"))
			Expect(tol["value"]).To(Equal("compute"))
		})
	})

	Context("Profile switch", func() {
		It("should replace resources when switching hardware profiles", func() {
			// Create two profiles
			hwpOld := newHardwareProfile("old-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "1",
						"defaultCount": "2",
						"resourceType": "CPU",
					},
					map[string]any{
						"displayName":  "Memory",
						"identifier":   "memory",
						"minCount":     "2Gi",
						"defaultCount": "4Gi",
						"resourceType": "Memory",
					},
				},
				"scheduling": map[string]any{
					"node": map[string]any{
						"nodeSelector": map[string]any{
							"old-key": "old-value",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwpOld)).To(Succeed())

			hwpNew := newHardwareProfile("new-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "4",
						"defaultCount": "8",
						"resourceType": "CPU",
					},
					map[string]any{
						"displayName":  "Memory",
						"identifier":   "memory",
						"minCount":     "8Gi",
						"defaultCount": "16Gi",
						"resourceType": "Memory",
					},
				},
				"scheduling": map[string]any{
					"node": map[string]any{
						"nodeSelector": map[string]any{
							"new-key": "new-value",
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwpNew)).To(Succeed())

			// Create notebook with old profile
			nb := newNotebook("nb-profile-switch", map[string]string{
				metadata.HardwareProfileNameAnnotation: "old-profile",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			// Verify old profile was applied
			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			containers, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).NotTo(BeEmpty())

			container, ok := containers[0].(map[string]any)
			Expect(ok).To(BeTrue())
			requests, _, _ := unstructured.NestedMap(container, "resources", "requests")
			Expect(requests).To(HaveKeyWithValue("cpu", "2"))

			// Switch to new profile
			created.SetAnnotations(map[string]string{
				metadata.HardwareProfileNameAnnotation:      "new-profile",
				metadata.HardwareProfileNamespaceAnnotation: testNamespace,
			})
			Expect(k8sClient.Update(ctx, created)).To(Succeed())

			// Fetch again to verify new profile was applied
			updated := &unstructured.Unstructured{}
			updated.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), updated)).To(Succeed())

			containers, found, err = unstructured.NestedSlice(updated.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).NotTo(BeEmpty())

			container, ok = containers[0].(map[string]any)
			Expect(ok).To(BeTrue())
			requests, _, _ = unstructured.NestedMap(container, "resources", "requests")
			Expect(requests).To(HaveKeyWithValue("cpu", "8"))
			Expect(requests).To(HaveKeyWithValue("memory", "16Gi"))

			// Verify new nodeSelector replaced old
			nodeSelector, found, _ := unstructured.NestedStringMap(updated.Object, "spec", "template", "spec", "nodeSelector")
			Expect(found).To(BeTrue())
			Expect(nodeSelector).To(HaveKeyWithValue("new-key", "new-value"))
			Expect(nodeSelector).NotTo(HaveKey("old-key"))
		})
	})

	Context("Profile removal", func() {
		It("should remove scheduling settings when HWP annotation is removed", func() {
			hwp := newHardwareProfile("removable-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "1",
						"defaultCount": "2",
						"resourceType": "CPU",
					},
				},
				"scheduling": map[string]any{
					"node": map[string]any{
						"nodeSelector": map[string]any{
							"removable-key": "value",
						},
						"tolerations": []any{
							map[string]any{
								"key":      "removable-tol",
								"operator": "Exists",
								"effect":   "NoSchedule",
							},
						},
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			nb := newNotebook("nb-removal", map[string]string{
				metadata.HardwareProfileNameAnnotation: "removable-profile",
			})
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			// Verify profile was applied
			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			nodeSelector, found, _ := unstructured.NestedStringMap(created.Object, "spec", "template", "spec", "nodeSelector")
			Expect(found).To(BeTrue())
			Expect(nodeSelector).To(HaveKeyWithValue("removable-key", "value"))

			// Remove the HWP annotation (keep namespace annotation so removal logic triggers)
			annotations := created.GetAnnotations()
			delete(annotations, metadata.HardwareProfileNameAnnotation)
			created.SetAnnotations(annotations)
			Expect(k8sClient.Update(ctx, created)).To(Succeed())

			// Verify scheduling settings were removed
			updated := &unstructured.Unstructured{}
			updated.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), updated)).To(Succeed())

			_, found, _ = unstructured.NestedStringMap(updated.Object, "spec", "template", "spec", "nodeSelector")
			Expect(found).To(BeFalse(), "nodeSelector should be removed after HWP removal")

			_, found, _ = unstructured.NestedSlice(updated.Object, "spec", "template", "spec", "tolerations")
			Expect(found).To(BeFalse(), "tolerations should be removed after HWP removal")
		})
	})

	Context("Container name validation", func() {
		It("should admit with warning when container name does not match notebook name", func() {
			hwp := newHardwareProfile("validation-profile", map[string]any{
				"identifiers": []any{
					map[string]any{
						"displayName":  "CPU",
						"identifier":   "cpu",
						"minCount":     "1",
						"defaultCount": "2",
						"resourceType": "CPU",
					},
				},
			})
			Expect(k8sClient.Create(ctx, hwp)).To(Succeed())

			// Create notebook with mismatched container name (multi-container)
			nb := &unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": gvk.Notebook.Group + "/" + gvk.Notebook.Version,
					"kind":       gvk.Notebook.Kind,
					"metadata": map[string]any{
						"name":      "nb-mismatch",
						"namespace": testNamespace,
						"annotations": map[string]any{
							metadata.HardwareProfileNameAnnotation: "validation-profile",
						},
					},
					"spec": map[string]any{
						"template": map[string]any{
							"spec": map[string]any{
								"containers": []any{
									map[string]any{
										"name":  "wrong-name",
										"image": "quay.io/test/notebook:latest",
									},
									map[string]any{
										"name":  "oauth-proxy",
										"image": "quay.io/test/oauth:latest",
									},
								},
							},
						},
					},
				},
			}
			// Notebook should still be admitted (webhook allows with warning)
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			// Verify resources were NOT injected
			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			containers, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).NotTo(BeEmpty())

			container, ok := containers[0].(map[string]any)
			Expect(ok).To(BeTrue())
			_, hasResources := container["resources"]
			Expect(hasResources).To(BeFalse(), "resources should NOT be injected on container name mismatch")

			// Verify a Warning event was emitted
			Eventually(func() bool {
				events := &corev1.EventList{}
				if err := k8sClient.List(ctx, events, client.InNamespace(testNamespace)); err != nil {
					return false
				}

				for i := range events.Items {
					if events.Items[i].Reason == "ContainerNameMismatch" &&
						events.Items[i].Type == corev1.EventTypeWarning &&
						events.Items[i].InvolvedObject.Name == "nb-mismatch" {
						Expect(events.Items[i].Message).To(ContainSubstring("Rename container to"),
							"event message should contain the rename hint")

						return true
					}
				}

				return false
			}, "5s", "200ms").Should(BeTrue(), "Expected a ContainerNameMismatch Warning event")
		})
	})

	Context("No hardware profile annotation", func() {
		It("should not modify Notebook without HWP annotation", func() {
			nb := newNotebook("nb-no-hwp", nil)
			Expect(k8sClient.Create(ctx, nb)).To(Succeed())

			created := &unstructured.Unstructured{}
			created.SetGroupVersionKind(gvk.Notebook)
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(nb), created)).To(Succeed())

			containers, found, err := unstructured.NestedSlice(created.Object, "spec", "template", "spec", "containers")
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeTrue())
			Expect(containers).NotTo(BeEmpty())

			container, ok := containers[0].(map[string]any)
			Expect(ok).To(BeTrue())
			_, hasResources := container["resources"]
			Expect(hasResources).To(BeFalse(), "resources should not be injected without HWP annotation")

			// Should not have HWP namespace annotation
			annotations := created.GetAnnotations()
			Expect(annotations).NotTo(HaveKey(metadata.HardwareProfileNamespaceAnnotation))
		})
	})

	Context("Non-existent hardware profile", func() {
		It("should deny Notebook creation referencing non-existent HWP", func() {
			nb := newNotebook("nb-missing-hwp", map[string]string{
				metadata.HardwareProfileNameAnnotation: "does-not-exist",
			})
			err := k8sClient.Create(ctx, nb)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not found"))
		})
	})
})
