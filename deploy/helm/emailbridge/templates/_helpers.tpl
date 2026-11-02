{{/*
Expand the name of the chart.
*/}}
{{- define "emailbridge.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "emailbridge.fullname" -}}
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

{{- define "emailbridge.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "emailbridge.labels" -}}
helm.sh/chart: {{ include "emailbridge.chart" . }}
{{ include "emailbridge.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "emailbridge.selectorLabels" -}}
app.kubernetes.io/name: {{ include "emailbridge.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "emailbridge.componentLabels" -}}
{{ include "emailbridge.labels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "emailbridge.componentSelectorLabels" -}}
{{ include "emailbridge.selectorLabels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{- define "emailbridge.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "emailbridge.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "emailbridge.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{- define "emailbridge.secretEnv" -}}
- name: EB_NODE_ID
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: EB_MONGO_URI_FILE
  value: /run/secrets/{{ .Values.secretFiles.mongo_uri }}
- name: EB_AUTH_SECRET_FILE
  value: /run/secrets/{{ .Values.secretFiles.auth_secret }}
- name: EB_KEYS_LOCAL_FILE
  value: /run/secrets/{{ .Values.secretFiles.keys }}
{{- if .Values.secretFiles.s3_access_key }}
- name: EB_S3_ACCESS_KEY_FILE
  value: /run/secrets/{{ .Values.secretFiles.s3_access_key }}
{{- end }}
{{- if .Values.secretFiles.s3_secret_key }}
- name: EB_S3_SECRET_KEY_FILE
  value: /run/secrets/{{ .Values.secretFiles.s3_secret_key }}
{{- end }}
{{- if .Values.secretFiles.nats_creds }}
- name: EB_NATS_CREDS_FILE
  value: /run/secrets/{{ .Values.secretFiles.nats_creds }}
{{- end }}
{{- end }}

{{- define "emailbridge.volumeMounts" -}}
- name: secrets
  mountPath: /run/secrets
  readOnly: true
- name: data
  mountPath: /data
- name: tmp
  mountPath: /tmp
{{- with .Values.extraVolumeMounts }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "emailbridge.volumes" -}}
- name: secrets
  secret:
    secretName: {{ required "existingSecret is required" .Values.existingSecret }}
    defaultMode: 0400
- name: tmp
  emptyDir: {}
{{- if .Values.persistence.enabled }}
- name: data
  persistentVolumeClaim:
    claimName: {{ include "emailbridge.fullname" . }}-data
{{- else }}
- name: data
  emptyDir: {}
{{- end }}
{{- with .Values.extraVolumes }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "emailbridge.probes" -}}
livenessProbe:
  httpGet:
    path: /healthz
    port: http
  initialDelaySeconds: 15
  periodSeconds: 20
  timeoutSeconds: 5
  failureThreshold: 3
readinessProbe:
  httpGet:
    path: /readyz
    port: http
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 6
{{- end }}
