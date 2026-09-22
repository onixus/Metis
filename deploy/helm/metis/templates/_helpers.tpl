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
- name: METIS_MIGRATE
  value: {{ .Values.config.migrateOnStart | quote }}
- name: METIS_JIRA_BASE_URL
  value: {{ .Values.config.jiraBaseURL | quote }}
- name: METIS_DELIVERY_PROVIDER
  value: {{ .Values.config.deliveryProvider | quote }}
- name: METIS_JIRA_FIELDS_FILE
  value: {{ .Values.config.jiraFieldsFile | quote }}
- name: METIS_JIRA_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .Values.secrets.existingSecret }}, key: jiraToken, optional: true } }
- name: METIS_CONFLUENCE_BASE_URL
  value: {{ .Values.config.confluenceBaseURL | quote }}
- name: METIS_KNOWLEDGE_PROVIDER
  value: {{ .Values.config.knowledgeProvider | quote }}
- name: METIS_CONFLUENCE_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .Values.secrets.existingSecret }}, key: confluenceToken, optional: true } }
- name: METIS_CONFLUENCE_SPACE
  value: {{ .Values.config.confluenceSpace | quote }}
- name: METIS_CRM_PROVIDER
  value: {{ .Values.config.crmProvider | quote }}
- name: METIS_CRM_DIR
  value: {{ .Values.config.crmDir | quote }}
- name: METIS_CRM_SYNC_INTERVAL
  value: {{ .Values.config.crmSyncInterval | quote }}
- name: METIS_BITRIX_BASE_URL
  value: {{ .Values.config.bitrixBaseURL | quote }}
- name: METIS_BITRIX_TOKEN
  valueFrom: { secretKeyRef: { name: {{ .Values.secrets.existingSecret }}, key: bitrixToken, optional: true } }
- name: METIS_BITRIX_FIELDS_FILE
  value: {{ .Values.config.bitrixFieldsFile | quote }}
- name: METIS_FINANCE_SOURCES_FILE
  value: {{ .Values.config.financeSourcesFile | quote }}
- name: METIS_FINANCE_SYNC_INTERVAL
  value: {{ .Values.config.financeSyncInterval | quote }}
- name: METIS_WORKLOG_TEAMS_FILE
  value: {{ .Values.config.worklogTeamsFile | quote }}
- name: METIS_OTEL_EXPORTER
  value: {{ .Values.config.otelExporter | quote }}
- name: METIS_LOG_LEVEL
  value: {{ .Values.config.logLevel | quote }}
- name: METIS_VERSION
  value: {{ .Chart.AppVersion | quote }}
- name: METIS_DELIVERY_SYNC_INTERVAL
  value: {{ .Values.config.deliverySyncInterval | quote }}
- name: METIS_RENEWAL_INTERVAL
  value: {{ .Values.config.renewalInterval | quote }}
{{- end -}}
{{- define "metis.integrationMounts" -}}
{{- if .Values.integrationFiles.profileConfigMap }}
- name: connector-profiles
  mountPath: /etc/metis
  readOnly: true
{{- end }}
{{- if .Values.integrationFiles.dataExistingClaim }}
- name: connector-data
  mountPath: /data
  readOnly: true
{{- end }}
{{- end -}}
{{- define "metis.integrationVolumes" -}}
{{- if .Values.integrationFiles.profileConfigMap }}
- name: connector-profiles
  configMap:
    name: {{ .Values.integrationFiles.profileConfigMap | quote }}
{{- end }}
{{- if .Values.integrationFiles.dataExistingClaim }}
- name: connector-data
  persistentVolumeClaim:
    claimName: {{ .Values.integrationFiles.dataExistingClaim | quote }}
    readOnly: true
{{- end }}
{{- end -}}
