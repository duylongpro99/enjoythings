{{- define "enjoythings.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "enjoythings.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- include "enjoythings.name" . -}}
{{- end -}}
{{- end -}}

{{- define "enjoythings.labels" -}}
app.kubernetes.io/name: {{ include "enjoythings.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "enjoythings.selectorLabels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "enjoythings.configName" -}}
{{ include "enjoythings.fullname" . }}-config
{{- end -}}

{{- define "enjoythings.secretName" -}}
{{ include "enjoythings.fullname" . }}-secret
{{- end -}}

{{- /* Secret written by the cert-manager Certificate for one service; takes the service name. */ -}}
{{- define "enjoythings.certSecretName" -}}
{{ . }}-mtls
{{- end -}}
