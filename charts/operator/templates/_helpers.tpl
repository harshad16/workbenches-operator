{{/*
Expand the name of the chart.
*/}}
{{- define "workbenches-operator.name" -}}
workbenches-operator
{{- end }}

{{/*
Resource name prefix (matches config/default namePrefix).
*/}}
{{- define "workbenches-operator.namePrefix" -}}
{{- .Values.namePrefix | default "workbenches-operator-" -}}
{{- end }}

{{/*
Prefixed resource name: {{ namePrefix }}<suffix>
Usage: include "workbenches-operator.prefixed" (list . "controller-manager")
*/}}
{{- define "workbenches-operator.prefixed" -}}
{{- $root := index . 0 -}}
{{- $suffix := index . 1 -}}
{{- printf "%s%s" (include "workbenches-operator.namePrefix" $root) $suffix -}}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "workbenches-operator.serviceAccountName" -}}
{{- include "workbenches-operator.prefixed" (list . .Values.serviceAccount.name) -}}
{{- end }}

{{/*
Deployment name. Must match ModuleHandler ReleaseName (workbenches-operator) so
the platform injectModuleEnv action can find this Deployment by metadata.name.
*/}}
{{- define "workbenches-operator.deploymentName" -}}
{{- .Release.Name -}}
{{- end }}

{{/*
ClusterRole names.
*/}}
{{- define "workbenches-operator.managerClusterRoleName" -}}
{{- include "workbenches-operator.prefixed" (list . "manager-role") -}}
{{- end }}

{{- define "workbenches-operator.escalateClusterRoleName" -}}
{{- include "workbenches-operator.prefixed" (list . "manager-rbac-escalate-role") -}}
{{- end }}

{{/*
Leader election Role name (namespace-scoped).
*/}}
{{- define "workbenches-operator.leaderElectionRoleName" -}}
{{- include "workbenches-operator.prefixed" (list . "leader-election-role") -}}
{{- end }}

{{/*
Common labels applied to operator resources.
*/}}
{{- define "workbenches-operator.labels" -}}
app.kubernetes.io/name: {{ include "workbenches-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: controller
control-plane: controller-manager
{{- end }}

{{/*
Selector labels for the Deployment pod template.
*/}}
{{- define "workbenches-operator.selectorLabels" -}}
control-plane: controller-manager
app.kubernetes.io/name: {{ include "workbenches-operator.name" . }}
{{- end }}

{{/*
Operator container arguments.
*/}}
{{- define "workbenches-operator.managerArgs" -}}
- --health-probe-bind-address=:8081
- --manifests-base-path={{ .Values.manifests.basePath }}
{{- if .Values.leaderElection.enabled }}
- --leader-elect
{{- end }}
{{- if ne .Values.metrics.bindAddress "0" }}
- --metrics-bind-address={{ .Values.metrics.bindAddress }}
{{- end }}
{{- if .Values.metrics.secure }}
- --metrics-secure=true
{{- else }}
- --metrics-secure=false
{{- end }}
{{- end }}

{{/*
Container environment variables.
*/}}
{{- define "workbenches-operator.managerEnv" -}}
- name: APPLICATIONS_NAMESPACE
  value: {{ .Values.applicationsNamespace | quote }}
{{- range $name, $value := .Values.relatedImages }}
{{- if $value }}
- name: {{ $name }}
  value: {{ $value | quote }}
{{- end }}
{{- end }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end }}
