{{- define "metis.name" -}}{{ .Chart.Name }}{{- end -}}
{{- define "metis.labels" -}}
app.kubernetes.io/name: {{ include "metis.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
{{- end -}}
{{- define "metis.env" -}}
- name: METIS_STORAGE
  value: postgres
- name: METIS_DATABASE_URL
  valueFrom: { secretKeyRef: { name: {{ .Values.secrets.existingSecret }}, key: databaseUrl } }
- name: METIS_JIRA_BASE_URL
  value: {{ .Values.config.jiraBaseURL | quote }}
- name: METIS_JIRA_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .Values.secrets.existingSecret }}, key: jiraToken, optional: true } }
- name: METIS_OTEL_EXPORTER
  value: {{ .Values.config.otelExporter | quote }}
- name: METIS_LOG_LEVEL
  value: {{ .Values.config.logLevel | quote }}
- name: METIS_VERSION
  value: {{ .Chart.AppVersion | quote }}
{{- end -}}
