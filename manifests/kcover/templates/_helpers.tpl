{{/*
Expand the name of the chart.
*/}}
{{- define "kcover.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kcover.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kcover.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kcover.labels" -}}
helm.sh/chart: {{ include "kcover.chart" . }}
{{ include "kcover.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kcover.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kcover.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/* Create the name of the agent service account to use. */}}
{{- define "kcover.agentServiceAccountName" -}}
{{- default (printf "%s-agent" (include "kcover.fullname" .) | trunc 63 | trimSuffix "-") .Values.agent.serviceAccount.name -}}
{{- end }}

{{/* Create the name of the controller service account to use. */}}
{{- define "kcover.controllerServiceAccountName" -}}
{{- default (printf "%s-controller" (include "kcover.fullname" .) | trunc 63 | trimSuffix "-") .Values.controller.serviceAccount.name -}}
{{- end }}

{{- define "controller.image" -}}
{{ include "common.images.image" (dict "imageRoot" .Values.controller.image "global" .Values.global "defaultTag" .Chart.AppVersion) }}
{{- end -}}

{{- define "kcover.agentFlavor" -}}
{{- $flavor := default "base" .Values.agent.flavor -}}
{{- if not (or (eq $flavor "base") (eq $flavor "metax")) -}}
{{- fail (printf "unsupported agent.flavor %q: supported values are base and metax" $flavor) -}}
{{- end -}}
{{- $flavor -}}
{{- end -}}

{{- define "agent.image" -}}
{{- $flavor := include "kcover.agentFlavor" . -}}
{{- $repository := .Values.agent.image.repository -}}
{{- if not $repository -}}
  {{- if eq $flavor "metax" -}}
    {{- $repository = "baizeai/kcover-agent-metax" -}}
  {{- else -}}
    {{- $repository = "baizeai/kcover-agent" -}}
  {{- end -}}
{{- end -}}
{{ include "common.images.image" (dict "imageRoot" (dict "registry" .Values.agent.image.registry "repository" $repository "tag" .Values.agent.image.tag) "global" .Values.global "defaultTag" .Chart.AppVersion) }}
{{- end -}}

{{- define "kcover.agentConfigMapName" -}}
{{- if .Values.agent.config.existingConfigMap -}}
{{- .Values.agent.config.existingConfigMap -}}
{{- else -}}
{{- printf "%s-agent-config" (include "kcover.fullname" .) -}}
{{- end -}}
{{- end -}}
