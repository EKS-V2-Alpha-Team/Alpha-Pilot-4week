{{/*
Resource name. Always the release name (e.g. "order-service") so that in-cluster
DNS matches the service URLs the Go code defaults to (http://order-service:8081).
Deliberately does NOT append the chart name like the `helm create` default.
*/}}
{{- define "microservice.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Chart name and version, used in the helm.sh/chart label.
*/}}
{{- define "microservice.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Selector labels. These end up in Deployment.spec.selector, which is immutable:
never add to or change this list after the first install.
*/}}
{{- define "microservice.selectorLabels" -}}
app.kubernetes.io/name: {{ include "microservice.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Common labels, applied to every resource.
*/}}
{{- define "microservice.labels" -}}
helm.sh/chart: {{ include "microservice.chart" . }}
{{ include "microservice.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | trunc 63 | trimSuffix "-" | quote }}
{{- with .Values.component }}
app.kubernetes.io/component: {{ . }}
{{- end }}
app.kubernetes.io/part-of: {{ .Values.partOf }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
ServiceAccount name. Fixed to the release name because EKS Pod Identity
associations in Terraform are keyed on namespace + this exact name.
*/}}
{{- define "microservice.serviceAccountName" -}}
{{- include "microservice.fullname" . }}
{{- end }}

{{/*
Container image reference. A digest wins over a tag when both are set.
*/}}
{{- define "microservice.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- end }}
{{- end }}

{{/*
Lowest number of pods the service runs with. Drives whether a PDB makes sense.
*/}}
{{- define "microservice.minReplicas" -}}
{{- if .Values.autoscaling.enabled -}}
{{ .Values.autoscaling.minReplicas }}
{{- else -}}
{{ .Values.replicaCount }}
{{- end }}
{{- end }}

{{/*
True (non-empty) when this service needs an ExternalSecret.
*/}}
{{- define "microservice.hasSecrets" -}}
{{- if or .Values.database.enabled .Values.externalSecrets.env -}}
true
{{- end }}
{{- end }}

{{/*
Guard rails for singleton workloads (e.g. scheduler, which runs cron on an
in-process ticker: two replicas would run every job twice).
*/}}
{{- define "microservice.validate" -}}
{{- if .Values.singleton }}
{{- if .Values.autoscaling.enabled }}
{{- fail "singleton: true cannot be combined with autoscaling.enabled" }}
{{- end }}
{{- if gt (int .Values.replicaCount) 1 }}
{{- fail "singleton: true requires replicaCount: 1" }}
{{- end }}
{{- end }}
{{- end }}
