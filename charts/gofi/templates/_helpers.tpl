{{/*
Expand the name of the chart.
*/}}
{{- define "gofi.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to
this (by the DNS naming spec).
*/}}
{{- define "gofi.fullname" -}}
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

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gofi.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gofi.labels" -}}
helm.sh/chart: {{ include "gofi.chart" . }}
{{ include "gofi.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gofi.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gofi.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "gofi.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gofi.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image references. A digest wins over the tag when set.
*/}}
{{- define "gofi.imageRef" -}}
{{- if .digest }}{{ .repository }}@{{ .digest }}{{ else }}{{ .repository }}:{{ .tag }}{{ end }}
{{- end }}

{{- define "gofi.image" -}}
{{ include "gofi.imageRef" .Values.image }}
{{- end }}

{{- define "gofi.sandboxImage" -}}
{{ include "gofi.imageRef" .Values.sandboxImage }}
{{- end }}

{{/*
Tooling image: toolingImage when a repository is set (tag defaults to the
sandbox tag), otherwise empty (the controller falls back to the sandbox image).
*/}}
{{- define "gofi.toolingImage" -}}
{{- with .Values.toolingImage }}
{{- if .repository }}
{{- include "gofi.imageRef" (dict "repository" .repository "tag" (default $.Values.sandboxImage.tag .tag) "digest" .digest) }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Kubernetes quantity to integer bytes (Ki/Mi/Gi/Ti, K/M/G/T, plain bytes).
*/}}
{{- define "gofi.quantityBytes" -}}
{{- $q := toString . -}}
{{- $units := dict "Ki" 1024 "Mi" 1048576 "Gi" 1073741824 "Ti" 1099511627776 "K" 1000 "k" 1000 "M" 1000000 "G" 1000000000 "T" 1000000000000 -}}
{{- $mult := 1 -}}
{{- $num := $q -}}
{{- range $suffix, $m := $units -}}
{{- if and (hasSuffix $suffix $q) (eq $mult 1) -}}
{{- $mult = $m -}}
{{- $num = trimSuffix $suffix $q -}}
{{- end -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+(\\.[0-9]+)?$" $num) -}}
{{- fail (printf "unsupported memory quantity %q" $q) -}}
{{- end -}}
{{- printf "%.0f" (mulf (float64 $num) (float64 $mult)) -}}
{{- end }}

{{/*
GOMEMLIMIT for the controller: goRuntime.goMemLimitRatio of limits.memory.
*/}}
{{- define "gofi.goMemLimit" -}}
{{- $bytes := include "gofi.quantityBytes" .Values.resources.limits.memory -}}
{{- printf "%.0f" (mulf (float64 $bytes) (float64 .Values.goRuntime.goMemLimitRatio)) -}}
{{- end }}

{{/*
PriorityClass names: explicit values win; otherwise the chart's classes when
it creates them; otherwise none.
*/}}
{{- define "gofi.controllerPriorityClassName" -}}
{{- if .Values.priorityClassName }}{{ .Values.priorityClassName }}{{ else if .Values.priorityClasses.create }}{{ .Values.priorityClasses.controlPlane.name }}{{ end }}
{{- end }}

{{- define "gofi.dataPlanePriorityClassName" -}}
{{- $explicit := index . 0 -}}
{{- $root := index . 1 -}}
{{- if $explicit }}{{ $explicit }}{{ else if $root.Values.priorityClasses.create }}{{ $root.Values.priorityClasses.dataPlane.name }}{{ end }}
{{- end }}

{{/*
Scheduling defaults handed to the controller for the Pods it creates
(GOFI_LISTENER_SCHEDULING / GOFI_WORKER_SCHEDULING).
*/}}
{{- define "gofi.podScheduling" -}}
{{- $cfg := index . 0 -}}
{{- $root := index . 1 -}}
{{- $out := dict -}}
{{- with include "gofi.dataPlanePriorityClassName" (list $cfg.priorityClassName $root) }}{{ $_ := set $out "priorityClassName" . }}{{ end -}}
{{- with $cfg.nodeSelector }}{{ $_ := set $out "nodeSelector" . }}{{ end -}}
{{- with $cfg.tolerations }}{{ $_ := set $out "tolerations" . }}{{ end -}}
{{- with $cfg.affinity }}{{ $_ := set $out "affinity" . }}{{ end -}}
{{- with $cfg.topologySpreadConstraints }}{{ $_ := set $out "topologySpreadConstraints" . }}{{ end -}}
{{- if not (kindIs "invalid" $cfg.terminationGracePeriodSeconds) }}{{ $_ := set $out "terminationGracePeriodSeconds" (int $cfg.terminationGracePeriodSeconds) }}{{ end -}}
{{- toJson $out -}}
{{- end }}

{{/*
Labels of Pods the controller creates.
*/}}
{{- define "gofi.listenerPodSelector" -}}
matchLabels:
  gofi.io/managed-by: "true"
{{- end }}

{{- define "gofi.workerPodSelector" -}}
matchLabels:
  app.kubernetes.io/name: gofi-python
  gofi.managed-by: gofi
{{- end }}

{{- define "gofi.controllerPodSelector" -}}
matchLabels:
  {{- include "gofi.selectorLabels" . | nindent 2 }}
  app.kubernetes.io/component: controller
{{- end }}

{{/*
Runbook link for an alert: alerting.runbookBaseUrl plus the alert's anchor.
*/}}
{{- define "gofi.runbook" -}}
{{- $root := index . 0 -}}
{{- $alert := index . 1 -}}
{{- $base := $root.Values.alerting.runbookBaseUrl -}}
{{- if contains "#" $base }}{{ printf "%s%s" $base (lower $alert) | quote }}{{ else }}{{ printf "%s#%s" $base (lower $alert) | quote }}{{ end }}
{{- end }}
