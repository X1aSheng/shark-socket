{{/*
Expand the name of the chart.
*/}}
{{- define "shark-socket.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shark-socket.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shark-socket.labels" -}}
helm.sh/chart: {{ include "shark-socket.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "shark-socket.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: shark-socket
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "shark-socket.selectorLabels" -}}
app.kubernetes.io/name: shark-socket
app.kubernetes.io/component: gateway
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Environment variables for the shark-socket container
*/}}
{{- define "shark-socket.env" -}}
- name: SHARK_LOG_LEVEL
  value: {{ .Values.config.logLevel | quote }}
- name: SHARK_LOG_FORMAT
  value: {{ .Values.config.logFormat | quote }}
- name: SHARK_METRICS_ADDR
  value: {{ .Values.metrics.addr | quote }}
- name: SHARK_METRICS_ENABLED
  value: {{ .Values.metrics.enabled | quote }}
- name: SHARK_SHUTDOWN_TIMEOUT
  value: {{ .Values.config.shutdownTimeout | quote }}
- name: SHARK_TCP_ENABLED
  value: {{ .Values.protocols.tcp.enabled | quote }}
- name: SHARK_TCP_PORT
  value: {{ .Values.protocols.tcp.port | quote }}
- name: SHARK_UDP_ENABLED
  value: {{ .Values.protocols.udp.enabled | quote }}
- name: SHARK_UDP_PORT
  value: {{ .Values.protocols.udp.port | quote }}
- name: SHARK_HTTP_ENABLED
  value: {{ .Values.protocols.http.enabled | quote }}
- name: SHARK_HTTP_PORT
  value: {{ .Values.protocols.http.port | quote }}
- name: SHARK_WS_ENABLED
  value: {{ .Values.protocols.websocket.enabled | quote }}
- name: SHARK_WS_PORT
  value: {{ .Values.protocols.websocket.port | quote }}
- name: SHARK_COAP_ENABLED
  value: {{ .Values.protocols.coap.enabled | quote }}
- name: SHARK_COAP_PORT
  value: {{ .Values.protocols.coap.port | quote }}
- name: SHARK_QUIC_ENABLED
  value: {{ .Values.protocols.quic.enabled | quote }}
- name: SHARK_QUIC_PORT
  value: {{ .Values.protocols.quic.port | quote }}
- name: SHARK_GRPCWEB_ENABLED
  value: {{ .Values.protocols.grpcweb.enabled | quote }}
- name: SHARK_GRPCWEB_PORT
  value: {{ .Values.protocols.grpcweb.port | quote }}
{{- end }}
