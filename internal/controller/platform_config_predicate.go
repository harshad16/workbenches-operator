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
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/opendatahub-io/workbenches-operator/internal/platformconfig"
)

type platformConfigChangedPredicate struct {
	applicationsNamespace string
}

func newPlatformConfigChangedPredicate(applicationsNamespace string) platformConfigChangedPredicate {
	return platformConfigChangedPredicate{applicationsNamespace: applicationsNamespace}
}

func (p platformConfigChangedPredicate) Create(e event.CreateEvent) bool {
	return p.matches(e.Object)
}

func (p platformConfigChangedPredicate) Update(e event.UpdateEvent) bool {
	if !p.matches(e.ObjectNew) {
		return false
	}

	oldCM, oldOK := e.ObjectOld.(*corev1.ConfigMap)
	newCM, newOK := e.ObjectNew.(*corev1.ConfigMap)
	if !oldOK || !newOK {
		return true
	}

	return platformConfigValue(oldCM) != platformConfigValue(newCM)
}

func (p platformConfigChangedPredicate) Delete(e event.DeleteEvent) bool {
	return p.matches(e.Object)
}

func (p platformConfigChangedPredicate) Generic(_ event.GenericEvent) bool {
	return false
}

func (p platformConfigChangedPredicate) matches(obj client.Object) bool {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return false
	}

	return cm.GetName() == platformconfig.ConfigMapName && cm.GetNamespace() == p.applicationsNamespace
}

func platformConfigValue(cm *corev1.ConfigMap) string {
	if cm == nil || cm.Data == nil {
		return ""
	}

	name := strings.TrimSpace(cm.Data[platformconfig.DistributionNameKey])
	version := strings.TrimSpace(cm.Data[platformconfig.DistributionVersionKey])
	platformVersion := strings.TrimSpace(cm.Data[platformconfig.VersionDataKey])

	return name + "\x00" + version + "\x00" + platformVersion
}
