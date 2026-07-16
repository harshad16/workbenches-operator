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

// Package notebook implements the mutating webhook for Notebook connection secrets.
// This is a faithful port of the opendatahub-operator webhook with minimal changes
// to adapt imports for the standalone workbenches-operator context.
package notebook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/opendatahub-io/workbenches-operator/internal/metadata"
)

var NotebookContainersPath = []string{"spec", "template", "spec", "containers"}

const (
	Create string = "create"
	Delete string = "delete"

	maxConnectionAnnotationLength = 4096
	maxConnectionSecrets          = 32
)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get
//+kubebuilder:webhook:path=/workbenches-connection-notebook,mutating=true,failurePolicy=fail,timeoutSeconds=5,groups=kubeflow.org,resources=notebooks,verbs=create;update,versions=v1,name=connection-notebook.opendatahub.io,sideEffects=None,admissionReviewVersions=v1

// NotebookWebhook implements a mutating webhook that injects connection secrets
// into Notebook resources based on the opendatahub.io/connections annotation.
type NotebookWebhook struct {
	Client    client.Client
	APIReader client.Reader
	Decoder   admission.Decoder
	Name      string
}

// NotebookSecretReference pairs a secret reference with an action (create or delete).
type NotebookSecretReference struct {
	Secret corev1.SecretReference
	Action string
}

var _ admission.Handler = &NotebookWebhook{}

// SetupWithManager registers the notebook webhook with the manager.
func (w *NotebookWebhook) SetupWithManager(mgr ctrl.Manager) error {
	hookServer := mgr.GetWebhookServer()
	hookServer.Register("/workbenches-connection-notebook", &webhook.Admission{
		Handler: w,
	})

	return nil
}

// Handle processes admission requests for Notebook create/update operations.
func (w *NotebookWebhook) Handle(ctx context.Context, req admission.Request) admission.Response {
	log := logf.FromContext(ctx)

	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		return admission.Allowed(fmt.Sprintf("Operation %s on %s allowed", req.Operation, req.Kind.Kind))
	}

	if w.Decoder == nil {
		log.Error(nil, "Decoder is nil - webhook not properly initialized")

		return admission.Errored(http.StatusInternalServerError, errors.New("webhook decoder not initialized"))
	}

	notebook := &unstructured.Unstructured{}
	if err := w.Decoder.Decode(req, notebook); err != nil {
		log.Error(err, "failed to decode object")

		return admission.Errored(http.StatusBadRequest, fmt.Errorf("failed to decode object: %w", err))
	}

	if !notebook.GetDeletionTimestamp().IsZero() {
		return admission.Allowed("Object marked for deletion, skipping connection logic")
	}

	validationResp, shouldInject, notebookSecretRefs := w.validateNotebookConnectionAnnotation(ctx, notebook, &req)
	if !validationResp.Allowed {
		return validationResp
	}

	if !shouldInject || notebookSecretRefs == nil {
		return admission.Allowed(fmt.Sprintf(
			"Connection annotation validation passed in namespace %s for %s, no injection needed",
			req.Namespace, req.Kind.Kind,
		))
	}

	injectionPerformed, obj, err := w.performConnectionInjection(notebook, notebookSecretRefs)
	if err != nil {
		log.Error(err, "Failed to perform connection injection")

		return admission.Errored(http.StatusInternalServerError, err)
	}

	if injectionPerformed {
		marshaledObj, err := json.Marshal(obj)
		if err != nil {
			log.Error(err, "Failed to marshal modified object")

			return admission.Errored(http.StatusInternalServerError, err)
		}

		return admission.PatchResponseFromRaw(req.Object.Raw, marshaledObj)
	}

	return admission.Allowed("No injection performed")
}

func (w *NotebookWebhook) validateNotebookConnectionAnnotation(
	ctx context.Context,
	nb *unstructured.Unstructured,
	req *admission.Request,
) (admission.Response, bool, []NotebookSecretReference) {
	log := logf.FromContext(ctx)

	annotationValue := getAnnotation(nb, metadata.ConnectionAnnotation)
	if req.Operation == admissionv1.Create && annotationValue == "" {
		return admission.Allowed(fmt.Sprintf(
			"Annotation '%s' not present or empty value, skipping validation",
			metadata.ConnectionAnnotation,
		)), false, nil
	}

	connectionSecrets, err := parseConnectionsAnnotation(annotationValue)
	if err != nil {
		log.Error(err, "failed to parse connections annotation", "annotationValue", annotationValue)

		return admission.Denied(fmt.Sprintf("failed to parse connections annotation: %v", err)), false, nil
	}

	secretExistsErrors, permissionsErrors, err := w.checkSecretsExistsAndUserHasPermissions(ctx, req, connectionSecrets)
	if err != nil {
		log.Error(err, "error verifying secret(s) exist or confirming user has get permissions for the secret(s)", "connectionSecrets", connectionSecrets)

		return admission.Errored(http.StatusInternalServerError, fmt.Errorf(
			"error verifying secret(s) exist/user has permissions for them %s: %w", connectionSecrets, err,
		)), false, nil
	}

	if len(secretExistsErrors) > 0 || len(permissionsErrors) > 0 {
		allInvalid := append(secretExistsErrors, permissionsErrors...) //nolint:gocritic // intentional append to new variable
		log.V(1).Info("connection secret validation failed",
			"notFoundOrOutOfNamespace", secretExistsErrors,
			"permissionDenied", permissionsErrors,
		)

		return admission.Denied(fmt.Sprintf(
			"the following connection secret(s) are invalid — they may not exist, may be outside "+
				"the Notebook's namespace, or you may lack permission to access them: %s",
			strings.Join(allInvalid, ", "),
		)), false, nil
	}

	notebookSecretRefs, err := w.getNotebookSecretRefs(ctx, req, connectionSecrets)
	if err != nil {
		return admission.Errored(http.StatusInternalServerError, fmt.Errorf(
			"failed to get notebook secret references: %w", err,
		)), false, nil
	}

	return admission.Allowed("Connection permissions validated successfully"), true, notebookSecretRefs
}

func (w *NotebookWebhook) getNotebookSecretRefs(
	ctx context.Context,
	req *admission.Request,
	secretRefs []corev1.SecretReference,
) ([]NotebookSecretReference, error) {
	var notebookSecretRefs []NotebookSecretReference

	if req.Operation == admissionv1.Update {
		oldSecretRefs, err := w.getOldConnectionSecrets(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to get old secret references: %w", err)
		}

		secretActions := determineSecretActions(oldSecretRefs, secretRefs)

		allRefs := append(secretRefs, oldSecretRefs...) //nolint:gocritic // intentional append to new variable
		for _, secretRef := range allRefs {
			notebookSecretRefs = append(notebookSecretRefs, NotebookSecretReference{
				Secret: secretRef,
				Action: secretActions[secretRefKey(secretRef)],
			})
		}
	} else {
		for _, secretRef := range secretRefs {
			notebookSecretRefs = append(notebookSecretRefs, NotebookSecretReference{
				Secret: secretRef,
				Action: Create,
			})
		}
	}

	return notebookSecretRefs, nil
}

// parseConnectionsAnnotation parses the connections annotation value into a list of secret references.
// The annotation value should be a comma-separated list of fully qualified secret names (namespace/name).
func parseConnectionsAnnotation(value string) ([]corev1.SecretReference, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	if len(value) > maxConnectionAnnotationLength {
		return nil, fmt.Errorf("connections annotation exceeds %d bytes", maxConnectionAnnotationLength)
	}

	parts := strings.Split(value, ",")
	secretRefs := make([]corev1.SecretReference, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		secretParts := strings.Split(part, "/")
		if len(secretParts) != 2 { //nolint:mnd
			return nil, fmt.Errorf("invalid secret reference format '%s' - expected 'namespace/name'", part)
		}

		namespace := strings.TrimSpace(secretParts[0])
		name := strings.TrimSpace(secretParts[1])

		if namespace == "" || name == "" {
			return nil, fmt.Errorf("invalid secret reference '%s' - namespace and name cannot be empty", part)
		}

		key := fmt.Sprintf("%s/%s", namespace, name)
		if _, ok := seen[key]; ok {
			continue
		}

		if len(secretRefs) >= maxConnectionSecrets {
			return nil, fmt.Errorf("too many connection secrets (max %d)", maxConnectionSecrets)
		}

		seen[key] = struct{}{}

		secretRefs = append(secretRefs, corev1.SecretReference{
			Name:      name,
			Namespace: namespace,
		})
	}

	return secretRefs, nil
}

func (w *NotebookWebhook) checkSecretsExistsAndUserHasPermissions(
	ctx context.Context,
	req *admission.Request,
	secretRefs []corev1.SecretReference,
) ([]string, []string, error) {
	secretExistsErrors, err := w.checkSecretExists(ctx, req, secretRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check secret exists: %w", err)
	}

	permissionErrors, err := w.checkUserHasPermission(ctx, req, secretRefs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check user has permission: %w", err)
	}

	return secretExistsErrors, permissionErrors, nil
}

func (w *NotebookWebhook) checkSecretExists(
	ctx context.Context,
	req *admission.Request,
	secretRefs []corev1.SecretReference,
) ([]string, error) {
	log := logf.FromContext(ctx)

	var secretExistsErrors []string

	for _, secretRef := range secretRefs {
		log.V(1).Info("checking that secret in the same namespace as the notebook CR")

		if secretRef.Namespace != req.Namespace {
			secretExistsErrors = append(secretExistsErrors, fmt.Sprintf("%s/%s", secretRef.Namespace, secretRef.Name))

			continue
		}

		log.V(1).Info("checking that secret exists", "secret", secretRef.Name, "namespace", secretRef.Namespace)

		if err := w.APIReader.Get(ctx, client.ObjectKey{Namespace: secretRef.Namespace, Name: secretRef.Name}, &corev1.Secret{}); err != nil {
			if k8serr.IsNotFound(err) {
				secretExistsErrors = append(secretExistsErrors, fmt.Sprintf("%s/%s", secretRef.Namespace, secretRef.Name))

				continue
			}

			log.Error(err, "failed to check if secret exists", "secret", secretRef.Name, "namespace", secretRef.Namespace)

			return nil, fmt.Errorf("failed to check if secret exists: %w", err)
		}
	}

	return secretExistsErrors, nil
}

func (w *NotebookWebhook) checkUserHasPermission(
	ctx context.Context,
	req *admission.Request,
	secretRefs []corev1.SecretReference,
) ([]string, error) {
	log := logf.FromContext(ctx)

	var permissionErrors []string

	for _, secretRef := range secretRefs {
		log.V(1).Info("checking permission for secret", "secret", secretRef.Name, "namespace", secretRef.Namespace)

		sar := &authorizationv1.SubjectAccessReview{
			Spec: authorizationv1.SubjectAccessReviewSpec{
				User:   req.UserInfo.Username,
				Groups: req.UserInfo.Groups,
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: secretRef.Namespace,
					Verb:      "get",
					Group:     "",
					Version:   "v1",
					Resource:  "secrets",
					Name:      secretRef.Name,
				},
			},
		}

		if err := w.Client.Create(ctx, sar); err != nil {
			log.Error(err, "failed to create SubjectAccessReview", "secret", secretRef.Name, "namespace", secretRef.Namespace)

			return nil, fmt.Errorf("failed to create SubjectAccessReview: %w", err)
		}

		if !sar.Status.Allowed {
			log.V(1).Info("user does not have permission to access secret",
				"secret", secretRef.Name,
				"namespace", secretRef.Namespace,
				"reason", sar.Status.Reason,
				"evaluationError", sar.Status.EvaluationError,
			)

			permissionErrors = append(permissionErrors, fmt.Sprintf("%s/%s", secretRef.Namespace, secretRef.Name))
		} else {
			log.V(1).Info("user has permission to access secret", "secret", secretRef.Name, "namespace", secretRef.Namespace)
		}
	}

	return permissionErrors, nil
}

func (w *NotebookWebhook) performConnectionInjection(
	nb *unstructured.Unstructured,
	notebookSecretRefs []NotebookSecretReference,
) (bool, *unstructured.Unstructured, error) {
	containers, found, err := unstructured.NestedSlice(nb.Object, NotebookContainersPath...)
	if err != nil {
		return false, nil, fmt.Errorf("failed to get containers array: %w", err)
	}

	if !found || len(containers) == 0 {
		return false, nil, errors.New("no containers found in notebook")
	}

	container, ok := containers[0].(map[string]any)
	if !ok {
		return false, nil, errors.New("first container is not a map[string]interface{}")
	}

	existingEnvFrom, _ := container["envFrom"].([]any)
	for _, nbSecretRef := range notebookSecretRefs {
		existingEnvFrom = handleConnectionSecret(nbSecretRef, existingEnvFrom)
	}

	container["envFrom"] = existingEnvFrom

	containers[0] = container

	if err := unstructured.SetNestedSlice(nb.Object, containers, NotebookContainersPath...); err != nil {
		return false, nil, fmt.Errorf("failed to set containers array: %w", err)
	}

	return true, nb, nil
}

// determineSecretActions compares old and current secret references to determine
// which secrets need to be created, updated, or deleted.
func determineSecretActions(oldSecretRefs, currentSecretRefs []corev1.SecretReference) map[string]string {
	actions := make(map[string]string)

	oldSecretsMap := make(map[string]corev1.SecretReference)
	currentSecretsMap := make(map[string]corev1.SecretReference)

	for _, secretRef := range oldSecretRefs {
		key := secretRefKey(secretRef)
		oldSecretsMap[key] = secretRef
	}

	for _, secretRef := range currentSecretRefs {
		key := secretRefKey(secretRef)
		currentSecretsMap[key] = secretRef

		if _, existsInOld := oldSecretsMap[key]; !existsInOld {
			actions[key] = Create
		}
	}

	for _, oldSecretRef := range oldSecretRefs {
		key := secretRefKey(oldSecretRef)
		if _, existsInCurrent := currentSecretsMap[key]; !existsInCurrent {
			actions[key] = Delete
		}
	}

	return actions
}

func secretRefKey(secretRef corev1.SecretReference) string {
	return fmt.Sprintf("%s/%s", secretRef.Namespace, secretRef.Name)
}

// handleConnectionSecret adds or removes a connection secret from the envFrom based on the secret action.
func handleConnectionSecret(nbSecretRef NotebookSecretReference, existingEnvFrom []any) []any {
	switch nbSecretRef.Action {
	case Create:
		secretEntry := map[string]any{
			"secretRef": map[string]any{
				"name": nbSecretRef.Secret.Name,
			},
		}

		existingEnvFrom = append(existingEnvFrom, secretEntry)
	case Delete:
		for i, entry := range existingEnvFrom {
			entryMap, ok := entry.(map[string]any)
			if !ok {
				continue
			}

			secretRef, hasSecret := entryMap["secretRef"]
			if !hasSecret {
				continue
			}

			secretRefMap, ok := secretRef.(map[string]any)
			if !ok {
				continue
			}

			if name, hasName := secretRefMap["name"]; hasName && name == nbSecretRef.Secret.Name {
				existingEnvFrom = append(existingEnvFrom[:i], existingEnvFrom[i+1:]...)

				break
			}
		}
	}

	return existingEnvFrom
}

func (w *NotebookWebhook) getOldConnectionSecrets(ctx context.Context, req *admission.Request) ([]corev1.SecretReference, error) {
	log := logf.FromContext(ctx)

	oldNotebook := &unstructured.Unstructured{}
	if err := w.Decoder.DecodeRaw(req.OldObject, oldNotebook); err != nil {
		log.Error(err, "failed to decode old notebook object")

		return nil, fmt.Errorf("failed to decode old notebook object: %w", err)
	}

	oldAnnotationValue := getAnnotation(oldNotebook, metadata.ConnectionAnnotation)
	if oldAnnotationValue == "" {
		return []corev1.SecretReference{}, nil
	}

	oldSecretRefs, err := parseConnectionsAnnotation(oldAnnotationValue)
	if err != nil {
		log.Error(err, "failed to parse old secret references", "annotationValue", oldAnnotationValue)

		return nil, fmt.Errorf("failed to parse old secret references: %w", err)
	}

	log.V(1).Info("Successfully parsed old secret references", "count", len(oldSecretRefs), "secretRefs", oldSecretRefs)

	return oldSecretRefs, nil
}

// getAnnotation returns the value of the given annotation key, or "" if not present.
func getAnnotation(obj *unstructured.Unstructured, key string) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}

	return annotations[key]
}
