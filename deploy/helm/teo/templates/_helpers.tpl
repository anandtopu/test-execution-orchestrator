{{/*
Common labels and helpers for all TEO components.
*/}}
{{- define "teo.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: teo
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: teo
{{- end -}}

{{- define "teo.componentLabels" -}}
{{ include "teo.labels" . }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "teo.image" -}}
{{- $reg := .Values.global.image.registry -}}
{{- $ns := .Values.global.image.namespace -}}
{{- $tag := .imageTag | default .Values.global.image.tag | default .Chart.AppVersion -}}
{{ printf "%s/%s/%s:%s" $reg $ns .repo $tag }}
{{- end -}}

{{- define "teo.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
