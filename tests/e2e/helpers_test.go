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

package e2e

import (
	"strings"
	"time"

	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsv1alpha1 "github.com/opendatahub-io/workbenches-operator/api/v1alpha1"
	"github.com/opendatahub-io/workbenches-operator/internal/gvk"
	metadata "github.com/opendatahub-io/workbenches-operator/internal/metadata"
)

const odhNotebookControllerManager = "odh-notebook-controller-manager"

const (
	timeout  = 3 * time.Minute
	interval = 5 * time.Second

	webhookTimeout  = 2 * time.Minute
	webhookInterval = 3 * time.Second

	defaultTestApplicationsNamespace = "opendatahub"
	// Legacy JupyterHub-era notebooks namespace field retained on the CR for
	// immutability/CEL coverage. Operand deploy uses APPLICATIONS_NAMESPACE.
	defaultTestLegacyWorkbenchNamespace = "e2e-legacy-notebooks"
	webhookTestNamespace                = "e2e-webhook-test"
)

// operandNamespace is where notebook-controller operands are deployed
// (APPLICATIONS_NAMESPACE). Set during lifecycle tests after the CR reconciles.
var operandNamespace string

func workbenchesCR() *componentsv1alpha1.Workbenches {
	return &componentsv1alpha1.Workbenches{
		ObjectMeta: metav1.ObjectMeta{
			Name: componentsv1alpha1.WorkbenchesInstanceName,
		},
		Spec: componentsv1alpha1.WorkbenchesSpec{
			ManagementState:    "Managed",
			WorkbenchNamespace: defaultTestLegacyWorkbenchNamespace,
			Platform:           "OpenDataHub",
		},
	}
}

func getWorkbenches() *componentsv1alpha1.Workbenches {
	wb := &componentsv1alpha1.Workbenches{}
	ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{
		Name: componentsv1alpha1.WorkbenchesInstanceName,
	}, wb)).To(Succeed())

	return wb
}

// updateWorkbenchesSpec re-fetches the CR and applies the mutator to avoid
// conflicts with the controller's concurrent status updates.
func updateWorkbenchesSpec(mutate func(*componentsv1alpha1.Workbenches)) {
	EventuallyWithOffset(1, func() error {
		wb := &componentsv1alpha1.Workbenches{}
		if err := k8sClient.Get(ctx, types.NamespacedName{
			Name: componentsv1alpha1.WorkbenchesInstanceName,
		}, wb); err != nil {
			return err
		}

		mutate(wb)

		return k8sClient.Update(ctx, wb)
	}, 30*time.Second, 1*time.Second).Should(Succeed())
}

func waitForCondition(condType string, status metav1.ConditionStatus) {
	EventuallyWithOffset(1, func() metav1.ConditionStatus {
		wb := &componentsv1alpha1.Workbenches{}

		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: componentsv1alpha1.WorkbenchesInstanceName,
		}, wb)
		if err != nil {
			return ""
		}

		cond := meta.FindStatusCondition(wb.Status.Conditions, condType)
		if cond == nil {
			return ""
		}

		return cond.Status
	}, timeout, interval).Should(Equal(status),
		"condition %s should be %s", condType, status)
}

func waitForPhase(phase string) {
	EventuallyWithOffset(1, func() string {
		wb := &componentsv1alpha1.Workbenches{}

		err := k8sClient.Get(ctx, types.NamespacedName{
			Name: componentsv1alpha1.WorkbenchesInstanceName,
		}, wb)
		if err != nil {
			return ""
		}

		return wb.Status.Phase
	}, timeout, interval).Should(Equal(phase), "phase should be %s", phase)
}

func newNotebook(name string, annotations map[string]string) *unstructured.Unstructured {
	nb := &unstructured.Unstructured{}
	nb.SetGroupVersionKind(notebookGVK())
	nb.SetName(name)
	nb.SetNamespace(webhookTestNamespace)

	if annotations != nil {
		nb.SetAnnotations(annotations)
	}

	_ = unstructured.SetNestedField(nb.Object, map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name":  name,
					"image": "jupyter/minimal-notebook:latest",
				},
			},
		},
	}, "spec", "template")

	return nb
}

func notebookGVK() schema.GroupVersionKind {
	return gvk.Notebook
}

func hwpGVK() schema.GroupVersionKind {
	return gvk.HardwareProfile
}

func newHardwareProfile(name string, spec map[string]any) *unstructured.Unstructured {
	hwp := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": gvk.HardwareProfile.Group + "/" + gvk.HardwareProfile.Version,
			"kind":       gvk.HardwareProfile.Kind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": webhookTestNamespace,
			},
			"spec": spec,
		},
	}

	return hwp
}

func ensureCreated(obj client.Object) {
	err := k8sClient.Create(ctx, obj)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}
}

// expectDriftRecovery deletes the first component-labeled object from list and
// waits for the operator to recreate it with a new UID.
func componentLabelSelector() client.MatchingLabels {
	return client.MatchingLabels{
		metadata.ComponentLabelKey: metadata.LabelTrue,
	}
}

func skipKustomizeGeneratedConfigMap(name string) bool {
	return strings.HasPrefix(name, "odh-notebook-controller-image-parameters")
}

func waitForMLflowEnabled(expected string) {
	ExpectWithOffset(1, operandNamespace).NotTo(BeEmpty())

	EventuallyWithOffset(1, func(g Gomega) {
		deploy := &appsv1.Deployment{}
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name:      odhNotebookControllerManager,
			Namespace: operandNamespace,
		}, deploy)).To(Succeed())

		var mlflowValue string

		for _, c := range deploy.Spec.Template.Spec.Containers {
			if c.Name != "manager" {
				continue
			}

			for _, env := range c.Env {
				if env.Name == "MLFLOW_ENABLED" {
					mlflowValue = env.Value

					break
				}
			}
		}

		g.Expect(mlflowValue).To(Equal(expected),
			"MLFLOW_ENABLED on %s should be %q", odhNotebookControllerManager, expected)
	}, timeout, interval).Should(Succeed())
}

// expectDriftRecoveryAll deletes every component-labeled object in list and
// waits for the operator to recreate each one (parity with odh-operator e2e).
func expectDriftRecoveryAll(
	kind string,
	list client.ObjectList,
	items func() []client.Object,
	newObj func() client.Object,
	skip func(client.Object) bool,
) {
	ExpectWithOffset(1, operandNamespace).NotTo(BeEmpty())

	ExpectWithOffset(1, k8sClient.List(ctx, list,
		client.InNamespace(operandNamespace),
		componentLabelSelector(),
	)).To(Succeed())

	objects := items()
	ExpectWithOffset(1, objects).NotTo(BeEmpty(),
		"at least one labeled %s should exist before drift test", kind)

	for _, target := range objects {
		if skip != nil && skip(target) {
			continue
		}

		deletedUID := target.GetUID()
		ExpectWithOffset(1, k8sClient.Delete(ctx, target)).To(Succeed())

		EventuallyWithOffset(1, func(g Gomega) {
			fresh := newObj()
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      target.GetName(),
				Namespace: target.GetNamespace(),
			}, fresh)).To(Succeed())
			g.Expect(fresh.GetUID()).NotTo(Equal(deletedUID),
				"recreated %s %s should have a new UID", kind, target.GetName())
		}, timeout, interval).Should(Succeed(),
			"operator should recreate deleted %s %s", kind, target.GetName())
	}
}
