{{- define "fin-mcp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fin-mcp.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "fin-mcp.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fin-mcp.labels" -}}
app.kubernetes.io/name: {{ include "fin-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "fin-mcp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fin-mcp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "fin-mcp.secretName" -}}
{{- .Values.secrets.existingSecret | default (printf "%s-secrets" (include "fin-mcp.fullname" .)) -}}
{{- end -}}

{{- define "fin-mcp.hasSecrets" -}}
{{- if or .Values.secrets.existingSecret .Values.secrets.bearerToken .Values.secrets.valkeyPassword .Values.secrets.privateKeyContent -}}true{{- end -}}
{{- end -}}
