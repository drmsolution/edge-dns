{{- define "edge-dns.name" -}}
{{- default .Chart.Name .Values.global.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "edge-dns.fullname" -}}
{{- if .Values.global.fullnameOverride }}
{{- .Values.global.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.global.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "edge-dns.labels" -}}
helm.sh/chart: {{ include "edge-dns.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "edge-dns.selectorLabels" -}}
app.kubernetes.io/name: {{ include "edge-dns.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "edge-dns.image" -}}
{{- $registry := .Values.global.imageRegistry -}}
{{- $repo := .Values.global.imageRepository -}}
{{- $tag := .Values.global.imageTag | default .Chart.AppVersion -}}
{{- printf "%s/%s:%s" $registry $repo $tag -}}
{{- end }}

{{- define "edge-dns.namespace" -}}
{{- .Values.global.namespace | default "edge-dns" -}}
{{- end }}

{{- define "redis.name" -}}redis{{- end }}
{{- define "clickhouse.name" -}}clickhouse{{- end }}
{{- define "edge-dns.componentName" -}}edge-dns{{- end }}
{{- define "admin-api.name" -}}admin-api{{- end }}
