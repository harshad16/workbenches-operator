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
	"errors"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// mergeDeployments copies user-customizable fields from a live Deployment (source)
// onto the rendered desired Deployment (target) before Server-Side Apply.
//
// Preserved fields (parity with opendatahub-operator deploy.MergeDeployments):
//   - per-container resources (matched by container name)
//   - spec.replicas
//
// This restores the pre-module-operator in-tree workbenches behavior, where
// deploy.NewAction applied MergeDeployments unless opendatahub.io/managed=true.
func mergeDeployments(source, target *unstructured.Unstructured) error {
	containersPath := []string{"spec", "template", "spec", "containers"}
	replicasPath := []string{"spec", "replicas"}

	sc, ok, err := unstructured.NestedFieldNoCopy(source.Object, containersPath...)
	if err != nil && ok {
		return err
	}

	tc, ok, err := unstructured.NestedFieldNoCopy(target.Object, containersPath...)
	if err != nil && ok {
		return err
	}

	resources := make(map[string]any)

	var sourceContainers []any
	if sc != nil {
		sourceContainers, ok = sc.([]any)
		if !ok {
			return errors.New("source containers field is not a slice")
		}
	}

	var targetContainers []any
	if tc != nil {
		targetContainers, ok = tc.([]any)
		if !ok {
			return errors.New("target containers field is not a slice")
		}
	}

	for i := range sourceContainers {
		m, ok := sourceContainers[i].(map[string]any)
		if !ok {
			return errors.New("source container entry is not a map")
		}

		name, ok := m["name"]
		if !ok {
			continue
		}

		r, ok := m["resources"]
		if !ok {
			r = make(map[string]any)
		}

		nameStr, ok := name.(string)
		if !ok {
			continue
		}

		resources[nameStr] = r
	}

	for i := range targetContainers {
		m, ok := targetContainers[i].(map[string]any)
		if !ok {
			return errors.New("target container entry is not a map")
		}

		name, ok := m["name"]
		if !ok {
			continue
		}

		nameStr, ok := name.(string)
		if !ok {
			continue
		}

		nr, ok := resources[nameStr]
		if !ok {
			continue
		}

		nrMap, ok := nr.(map[string]any)
		if !ok {
			return errors.New("source container resources field is not a map")
		}

		if len(nrMap) == 0 {
			delete(m, "resources")
		} else {
			m["resources"] = nr
		}
	}

	sourceReplica, ok, err := unstructured.NestedFieldNoCopy(source.Object, replicasPath...)
	if err != nil {
		return err
	}

	if !ok {
		unstructured.RemoveNestedField(target.Object, replicasPath...)
	} else if err := unstructured.SetNestedField(target.Object, sourceReplica, replicasPath...); err != nil {
		return err
	}

	return nil
}
