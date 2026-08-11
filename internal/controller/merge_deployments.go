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
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var (
	deploymentContainersPath = []string{"spec", "template", "spec", "containers"}
	deploymentReplicasPath   = []string{"spec", "replicas"}
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
	if err := mergeDeploymentContainerResources(source, target); err != nil {
		return err
	}

	return mergeDeploymentReplicas(source, target)
}

func mergeDeploymentContainerResources(source, target *unstructured.Unstructured) error {
	sourceContainers, err := nestedContainerSlice(source, "source")
	if err != nil {
		return err
	}

	targetContainers, err := nestedContainerSlice(target, "target")
	if err != nil {
		return err
	}

	resourcesByName, err := containerResourcesByName(sourceContainers)
	if err != nil {
		return err
	}

	return applyContainerResources(targetContainers, resourcesByName)
}

func nestedContainerSlice(obj *unstructured.Unstructured, side string) ([]any, error) {
	raw, _, err := unstructured.NestedFieldNoCopy(obj.Object, deploymentContainersPath...)
	if err != nil {
		return nil, fmt.Errorf("%s containers path: %w", side, err)
	}

	if raw == nil {
		return nil, nil
	}

	containers, isSlice := raw.([]any)
	if !isSlice {
		return nil, fmt.Errorf("%s containers field is not a slice", side)
	}

	return containers, nil
}

func containerResourcesByName(containers []any) (map[string]any, error) {
	resourcesByName := make(map[string]any, len(containers))

	for i := range containers {
		container, isMap := containers[i].(map[string]any)
		if !isMap {
			return nil, errors.New("source container entry is not a map")
		}

		name, hasName := container["name"]
		if !hasName {
			continue
		}

		nameStr, isString := name.(string)
		if !isString {
			continue
		}

		resources, hasResources := container["resources"]
		if !hasResources {
			resources = make(map[string]any)
		}

		resourcesByName[nameStr] = resources
	}

	return resourcesByName, nil
}

func applyContainerResources(containers []any, resourcesByName map[string]any) error {
	for i := range containers {
		container, isMap := containers[i].(map[string]any)
		if !isMap {
			return errors.New("target container entry is not a map")
		}

		name, hasName := container["name"]
		if !hasName {
			continue
		}

		nameStr, isString := name.(string)
		if !isString {
			continue
		}

		resources, found := resourcesByName[nameStr]
		if !found {
			continue
		}

		resourcesMap, isResourcesMap := resources.(map[string]any)
		if !isResourcesMap {
			return errors.New("source container resources field is not a map")
		}

		if len(resourcesMap) == 0 {
			delete(container, "resources")
		} else {
			container["resources"] = resources
		}
	}

	return nil
}

func mergeDeploymentReplicas(source, target *unstructured.Unstructured) error {
	sourceReplica, found, err := unstructured.NestedFieldNoCopy(source.Object, deploymentReplicasPath...)
	if err != nil {
		return fmt.Errorf("source replicas path: %w", err)
	}

	if !found {
		unstructured.RemoveNestedField(target.Object, deploymentReplicasPath...)

		return nil
	}

	if err := unstructured.SetNestedField(target.Object, sourceReplica, deploymentReplicasPath...); err != nil {
		return fmt.Errorf("set target replicas: %w", err)
	}

	return nil
}
