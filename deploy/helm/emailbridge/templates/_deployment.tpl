{{- define "emailbridge.deployment" -}}
{{- $root := .root -}}
{{- $name := .name -}}
{{- $role := .role -}}
{{- $component := .component -}}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "emailbridge.fullname" $root }}-{{ $component }}
  labels:
    {{- include "emailbridge.componentLabels" (dict "root" $root "component" $component) | nindent 4 }}
spec:
  {{- $as := index $root.Values.autoscaling $component | default dict }}
  {{- if not ($as.enabled | default false) }}
  replicas: {{ $role.replicas }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "emailbridge.componentSelectorLabels" (dict "root" $root "component" $component) | nindent 6 }}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  template:
    metadata:
      labels:
        {{- include "emailbridge.componentSelectorLabels" (dict "root" $root "component" $component) | nindent 8 }}
      annotations:
        checksum/config: {{ include (print $root.Template.BasePath "/configmap.yaml") $root | sha256sum }}
    spec:
      serviceAccountName: {{ include "emailbridge.serviceAccountName" $root }}
      securityContext:
        {{- toYaml $root.Values.podSecurityContext | nindent 8 }}
      # No initContainers — migrations run in the main entrypoint under the lock.
      terminationGracePeriodSeconds: {{ $root.Values.terminationGracePeriodSeconds }}
      containers:
        - name: emailbridge
          image: {{ include "emailbridge.image" $root }}
          imagePullPolicy: {{ $root.Values.image.pullPolicy }}
          args: ["serve"]
          securityContext:
            {{- toYaml $root.Values.securityContext | nindent 12 }}
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: metrics
              containerPort: 9090
              protocol: TCP
          env:
            - name: EB_ROLES
              value: {{ $role.roles | quote }}
            {{- include "emailbridge.secretEnv" $root | nindent 12 }}
            {{- with $root.Values.extraEnv }}
            {{- toYaml . | nindent 12 }}
            {{- end }}
          envFrom:
            - configMapRef:
                name: {{ include "emailbridge.fullname" $root }}
          volumeMounts:
            {{- include "emailbridge.volumeMounts" $root | nindent 12 }}
          resources:
            {{- toYaml (index $root.Values.resources $component) | nindent 12 }}
          {{- include "emailbridge.probes" $root | nindent 10 }}
      volumes:
        {{- include "emailbridge.volumes" $root | nindent 8 }}
      {{- with $root.Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $root.Values.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
{{- end }}
