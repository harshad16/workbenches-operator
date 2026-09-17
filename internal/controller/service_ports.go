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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultServiceProtocol = "TCP"

type servicePortKey struct {
	port     int64
	protocol string
}

type parsedServicePort struct {
	key  servicePortKey
	name string
}

// prepareServiceForSSA rewrites live Service ports when a Server-Side Apply of
// the rendered manifest would produce duplicate spec.ports[].name values.
//
// Service ports are merged by {port, protocol}, but Kubernetes also requires
// port names to be unique. Changing a port number while keeping the same name
// (for example odh-notebook-controller-service metrics 8080 → 8443) therefore
// fails SSA with: spec.ports[1].name: Duplicate value: "metrics".
// See kubernetes/kubernetes#131147.
//
// When that collision is detected, spec.ports is replaced with the rendered
// list via a JSON merge patch (arrays are replaced, not strategically merged)
// so the subsequent SSA apply can succeed.
func (r *WorkbenchesReconciler) prepareServiceForSSA(
	ctx context.Context,
	obj *unstructured.Unstructured,
) error {
	if obj.GetKind() != kindService {
		return nil
	}

	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(obj.GroupVersionKind())

	err := r.Get(ctx, client.ObjectKeyFromObject(obj), live)
	if apierrors.IsNotFound(err) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to get live Service: %w", err)
	}

	if !ssaServicePortsDuplicateNames(live, obj) {
		return nil
	}

	desiredPorts, found, err := unstructured.NestedSlice(obj.Object, "spec", "ports")
	if err != nil {
		return fmt.Errorf("failed to read desired Service ports: %w", err)
	}

	if !found {
		desiredPorts = []any{}
	}

	original := live.DeepCopy()
	if err := unstructured.SetNestedSlice(live.Object, desiredPorts, "spec", "ports"); err != nil {
		return fmt.Errorf("failed to set desired Service ports: %w", err)
	}

	log.FromContext(ctx).Info("replacing Service ports before SSA to avoid duplicate port names",
		"name", obj.GetName(),
		"namespace", obj.GetNamespace())

	if err := r.Patch(ctx, live, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("failed to replace Service ports: %w", err)
	}

	return nil
}

// ssaServicePortsDuplicateNames reports whether a structured-merge of live and
// desired Service ports (keyed by port+protocol, desired wins) would contain
// two entries with the same non-empty name.
func ssaServicePortsDuplicateNames(live, desired *unstructured.Unstructured) bool {
	merged := map[servicePortKey]string{}

	for _, port := range parseServicePorts(live) {
		merged[port.key] = port.name
	}

	for _, port := range parseServicePorts(desired) {
		merged[port.key] = port.name
	}

	seen := make(map[string]struct{}, len(merged))

	for _, name := range merged {
		if name == "" {
			continue
		}

		if _, exists := seen[name]; exists {
			return true
		}

		seen[name] = struct{}{}
	}

	return false
}

func parseServicePorts(obj *unstructured.Unstructured) []parsedServicePort {
	raw, found, err := unstructured.NestedSlice(obj.Object, "spec", "ports")
	if err != nil || !found {
		return nil
	}

	ports := make([]parsedServicePort, 0, len(raw))

	for _, item := range raw {
		portMap, ok := item.(map[string]any)
		if !ok {
			continue
		}

		port, ok := asInt64(portMap["port"])
		if !ok {
			continue
		}

		protocol, _ := portMap["protocol"].(string)
		if protocol == "" {
			protocol = defaultServiceProtocol
		}

		name, _ := portMap["name"].(string)
		ports = append(ports, parsedServicePort{
			key:  servicePortKey{port: port, protocol: protocol},
			name: name,
		})
	}

	return ports
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int32:
		return int64(n), true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}
