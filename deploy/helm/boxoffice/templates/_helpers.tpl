{{- define "boxoffice.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "boxoffice.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "boxoffice.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: server
{{- end -}}

{{- define "boxoffice.postgresql.fullname" -}}
{{- printf "%s-postgresql" (include "boxoffice.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* True when Box Office uses PostgreSQL rather than SQLite. */}}
{{- define "boxoffice.usesPostgres" -}}
{{- if or .Values.postgresql.enabled .Values.database.url .Values.database.existingSecret -}}
true
{{- end -}}
{{- end -}}

{{/* The secret holding database-url (and the bundled database's password). */}}
{{- define "boxoffice.databaseSecret" -}}
{{- if .Values.database.existingSecret -}}
{{- .Values.database.existingSecret -}}
{{- else -}}
{{- printf "%s-db" (include "boxoffice.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/* True when an install hook generates the bundled database's credentials. */}}
{{- define "boxoffice.generateDBSecret" -}}
{{- if and .Values.postgresql.enabled (not .Values.database.existingSecret) (not .Values.postgresql.auth.password) -}}
true
{{- end -}}
{{- end -}}

{{- define "boxoffice.apiKeySecret" -}}
{{- if .Values.apiKey.existingSecret -}}
{{- .Values.apiKey.existingSecret -}}
{{- else if .Values.apiKey.value -}}
{{- printf "%s-api" (include "boxoffice.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "boxoffice.tmdbSecret" -}}
{{- if .Values.tmdb.existingSecret -}}
{{- .Values.tmdb.existingSecret -}}
{{- else if .Values.tmdb.apiKey -}}
{{- printf "%s-tmdb" (include "boxoffice.fullname" .) -}}
{{- end -}}
{{- end -}}
