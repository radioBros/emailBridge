<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiChevronDown, mdiChevronUp, mdiDelete, mdiPlus } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { EndpointCreate, EndpointUpdate, EndpointView, WebhookHeader } from "@/api/webhooks";
import { listFeatureSwitches } from "@/api/settings";
import { actionOf } from "@/constants/Actions";
import { required, positiveInteger } from "@/constants/Validations";
import {
  WEBHOOK_EVENT_TYPES,
  WEBHOOK_KINDS,
  defaultDelivery,
  defaultRetry,
} from "@/features/webhooks/eventTypes";
import AppDialog from "@/components/AppDialog.vue";
import type { EndpointRule } from "@/api/rules";
import RuleSummary from "@/features/rules/components/RuleSummary.vue";


const props = defineProps<{
  modelValue: boolean;
  endpoint: EndpointView | null;
  submitting: boolean;
  errorMsg: string | null;
  fieldErrors: Record<string, string>;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: EndpointCreate | (EndpointUpdate & { id: string })];
}>();

const { t } = useI18n();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const deliveryOpen = ref(false);
const mtlsOpen = ref(false);
const mtlsEnabled = ref(false);
const saveAction = actionOf("save");
const createAction = actionOf("create");
const deleteAction = actionOf("delete");

interface HeaderRow {
  name: string;
  value: string;
  secret: boolean;
}

const SECRET_SET = "[set]";

const form = reactive({
  name: "",
  kind: "http" as EndpointView["kind"], // integration endpoints are created by the integrations page
  url: "",
  enabled: true,
  events: [] as string[],
  text: "none" as "none" | "plain" | "html" | "both",
  payloadHeaders: [] as string[],
  attachments: false,
  webSafeHtml: false,
  signatureMode: "standard" as "standard" | "bodyOnly",
  maxAttempts: 10,
  backoffBase: "5s",
  backoffJitter: 0.2,
  timeout: "30s",
  ordering: "strict" as "strict" | "bestEffort",
  maxConcurrency: 8,
  contentType: "json" as "json" | "cloudevents",
  retryOn4xx: false,
  autoDisableAfter: "120h",
  headers: [] as HeaderRow[],
  // sink
  natsUrl: "",
  natsSubject: "eb.{type}",
  natsJetstream: false,
  natsCreds: "",
  natsCredsSet: false,
  kafkaBrokers: "",
  kafkaTopic: "",
  kafkaTls: false,
  kafkaSaslMechanism: "",
  kafkaSaslUsername: "",
  kafkaSaslPassword: "",
  kafkaSaslPasswordSet: false,
  sqsQueueUrl: "",
  sqsRegion: "",
  sqsEndpoint: "",
  sqsAccessKeyId: "",
  sqsSecretAccessKey: "",
  sqsSecretSet: false,
  snsTopicArn: "",
  snsRegion: "",
  snsEndpoint: "",
  snsAccessKeyId: "",
  snsSecretAccessKey: "",
  snsSecretSet: false,
  pubsubProject: "",
  pubsubTopic: "",
  pubsubCredentialsJSON: "",
  pubsubCredsSet: false,
  pubsubEmulatorHost: "",
  // mTLS
  tlsClientCert: "",
  tlsClientKey: "",
  tlsClientKeySet: false,
  tlsCaBundle: "",
});

const isEdit = computed(() => Boolean(props.endpoint));
const endpointRule = computed(
  (): EndpointRule | undefined => (props.endpoint as (EndpointView & { rule?: EndpointRule }) | null)?.rule,
);
const isHTTP = computed(() => form.kind === "http");

const kindItems = computed(() =>
  WEBHOOK_KINDS.map((k) => ({ title: t(`webhooks.kind_${k}`), value: k })),
);

const eventItems = computed(() =>
  WEBHOOK_EVENT_TYPES.map((e) => ({ title: e, value: e })),
);

const textItems = computed(() => [
  { title: t("webhooks.payload_text_none"), value: "none" },
  { title: t("webhooks.payload_text_plain"), value: "plain" },
  { title: t("webhooks.payload_text_html"), value: "html" },
  { title: t("webhooks.payload_text_both"), value: "both" },
]);

const modeItems = computed(() => [
  { title: t("webhooks.security_standard"), value: "standard" },
  { title: t("webhooks.security_body_only"), value: "bodyOnly" },
]);

const orderingItems = computed(() => [
  { title: t("webhooks.delivery_ordering_strict"), value: "strict" },
  { title: t("webhooks.delivery_ordering_best"), value: "bestEffort" },
]);

const contentTypeItems = computed(() => [
  { title: t("webhooks.delivery_content_json"), value: "json" },
  { title: t("webhooks.delivery_content_ce"), value: "cloudevents" },
]);

const saslItems = computed(() => [
  { title: t("webhooks.sasl_none"), value: "" },
  { title: "PLAIN", value: "PLAIN" },
  { title: "SCRAM-SHA-256", value: "SCRAM-SHA-256" },
  { title: "SCRAM-SHA-512", value: "SCRAM-SHA-512" },
]);

const secretHint = (set: boolean): string =>
  set ? t("webhooks.secret_set_hint") : "";

const resetSink = (): void => {
  form.natsUrl = "";
  form.natsSubject = "eb.{type}";
  form.natsJetstream = false;
  form.natsCreds = "";
  form.natsCredsSet = false;
  form.kafkaBrokers = "";
  form.kafkaTopic = "";
  form.kafkaTls = false;
  form.kafkaSaslMechanism = "";
  form.kafkaSaslUsername = "";
  form.kafkaSaslPassword = "";
  form.kafkaSaslPasswordSet = false;
  form.sqsQueueUrl = "";
  form.sqsRegion = "";
  form.sqsEndpoint = "";
  form.sqsAccessKeyId = "";
  form.sqsSecretAccessKey = "";
  form.sqsSecretSet = false;
  form.snsTopicArn = "";
  form.snsRegion = "";
  form.snsEndpoint = "";
  form.snsAccessKeyId = "";
  form.snsSecretAccessKey = "";
  form.snsSecretSet = false;
  form.pubsubProject = "";
  form.pubsubTopic = "";
  form.pubsubCredentialsJSON = "";
  form.pubsubCredsSet = false;
  form.pubsubEmulatorHost = "";
  form.tlsClientCert = "";
  form.tlsClientKey = "";
  form.tlsClientKeySet = false;
  form.tlsCaBundle = "";
};

const reset = (): void => {
  const d = defaultRetry();
  const del = defaultDelivery();
  form.name = "";
  form.kind = "http";
  form.url = "";
  form.enabled = true;
  form.events = [];
  form.text = "none";
  form.payloadHeaders = [];
  form.attachments = false;
  form.webSafeHtml = false;
  form.signatureMode = "standard";
  form.maxAttempts = d.maxAttempts;
  form.backoffBase = d.backoffBase;
  form.backoffJitter = d.backoffJitter;
  form.timeout = d.timeout;
  form.ordering = del.ordering;
  form.maxConcurrency = del.maxConcurrency;
  form.contentType = del.contentType;
  form.retryOn4xx = del.retryOn4xx;
  form.autoDisableAfter = del.autoDisableAfter;
  form.headers = [];
  deliveryOpen.value = false;
  mtlsOpen.value = false;
  resetSink();
};

const str = (v: unknown): string => (typeof v === "string" ? v : "");
const isSet = (v: unknown): boolean => v === SECRET_SET || (typeof v === "string" && v === SECRET_SET);

const fillSink = (ep: EndpointView): void => {
  const sc = (ep.sinkConfig ?? {}) as Record<string, unknown>;
  if (ep.kind === "nats") {
    form.natsUrl = str(sc.url);
    form.natsSubject = str(sc.subject) || "eb.{type}";
    form.natsJetstream = Boolean(sc.jetstream);
    form.natsCredsSet = isSet(sc.creds);
    form.natsCreds = "";
  } else if (ep.kind === "kafka") {
    const brokers = Array.isArray(sc.brokers) ? sc.brokers.map(String) : [];
    form.kafkaBrokers = brokers.join(", ");
    form.kafkaTopic = str(sc.topic);
    form.kafkaTls = Boolean(sc.tls);
    form.kafkaSaslMechanism = str(sc.saslMechanism);
    form.kafkaSaslUsername = str(sc.saslUsername);
    form.kafkaSaslPasswordSet = isSet(sc.saslPassword);
    form.kafkaSaslPassword = "";
  } else if (ep.kind === "sqs") {
    form.sqsQueueUrl = str(sc.queueUrl);
    form.sqsRegion = str(sc.region);
    form.sqsEndpoint = str(sc.endpoint);
    form.sqsAccessKeyId = str(sc.accessKeyId);
    form.sqsSecretSet = isSet(sc.secretAccessKey);
    form.sqsSecretAccessKey = "";
  } else if (ep.kind === "sns") {
    form.snsTopicArn = str(sc.topicArn);
    form.snsRegion = str(sc.region);
    form.snsEndpoint = str(sc.endpoint);
    form.snsAccessKeyId = str(sc.accessKeyId);
    form.snsSecretSet = isSet(sc.secretAccessKey);
    form.snsSecretAccessKey = "";
  } else if (ep.kind === "pubsub") {
    form.pubsubProject = str(sc.project);
    form.pubsubTopic = str(sc.topic);
    form.pubsubCredsSet = isSet(sc.credentialsJSON);
    form.pubsubCredentialsJSON = "";
    form.pubsubEmulatorHost = str(sc.emulatorHost);
  }
  const tls = ep.security?.tls as
    | { clientCert?: string; clientKey?: string; caBundle?: string }
    | undefined;
  if (tls?.clientCert) {
    form.tlsClientCert = tls.clientCert;
    form.tlsClientKeySet = isSet(tls.clientKey) || Boolean(tls.clientKey);
    form.tlsClientKey = "";
    form.tlsCaBundle = tls.caBundle ?? "";
    mtlsOpen.value = true;
  }
};

const fill = (ep: EndpointView): void => {
  const del = defaultDelivery();
  resetSink();
  form.name = ep.name;
  form.kind = ep.kind;
  form.url = ep.url ?? "";
  form.enabled = ep.enabled;
  form.events = [...(ep.events ?? [])];
  form.text = ep.payload?.text ?? "none";
  form.payloadHeaders = [...(ep.payload?.headers ?? [])];
  form.attachments = Boolean(ep.payload?.attachments);
  form.webSafeHtml = Boolean(ep.payload?.webSafeHtml);
  form.signatureMode = ep.security?.signatureMode ?? "standard";
  form.maxAttempts = ep.retry?.maxAttempts ?? 10;
  form.backoffBase = ep.retry?.backoffBase ?? "5s";
  form.backoffJitter = ep.retry?.backoffJitter ?? 0.2;
  form.timeout = ep.retry?.timeout ?? "30s";
  form.ordering = ep.delivery?.ordering ?? del.ordering;
  form.maxConcurrency = ep.delivery?.maxConcurrency ?? del.maxConcurrency;
  form.contentType = ep.delivery?.contentType ?? del.contentType;
  form.retryOn4xx = ep.delivery?.retryOn4xx ?? del.retryOn4xx;
  form.autoDisableAfter = ep.delivery?.autoDisableAfter ?? del.autoDisableAfter;
  form.headers = (ep.headers ?? []).map((h) => ({
    name: h.name,
    value: h.value ?? "",
    secret: Boolean(h.secret),
  }));
  deliveryOpen.value = false;
  mtlsOpen.value = false;
  fillSink(ep);
};

const loadMTLSSwitch = async (): Promise<void> => {
  try {
    const res = await listFeatureSwitches();
    const sw = (res.data ?? []).find((s) => s.name === "webhook.mtls");
    mtlsEnabled.value = Boolean(sw?.effective);
  } catch {
    mtlsEnabled.value = false;
  }
};

onMounted(() => {
  void loadMTLSSwitch();
});

watch(
  () => [props.modelValue, props.endpoint] as const,
  ([open, ep]) => {
    if (!open) return;
    void loadMTLSSwitch();
    if (ep) fill(ep);
    else reset();
  },
);

const addHeader = (): void => {
  form.headers.push({ name: "", value: "", secret: false });
};

const removeHeader = (idx: number): void => {
  form.headers.splice(idx, 1);
};

const normalizePayloadHeaders = (raw: unknown): string[] => {
  const list = Array.isArray(raw) ? raw : [];
  const out: string[] = [];
  const seen = new Set<string>();
  for (const item of list) {
    const name = String(item ?? "")
      .trim()
      .toLowerCase();
    if (!name || seen.has(name)) continue;
    seen.add(name);
    out.push(name);
  }
  return out;
};

const onPayloadHeaders = (raw: unknown): void => {
  form.payloadHeaders = normalizePayloadHeaders(raw);
};

const secretOrKeep = (value: string, wasSet: boolean): string | undefined => {
  const v = value.trim();
  if (v) return v;
  if (wasSet) return SECRET_SET;
  return undefined;
};

const buildSinkConfig = (): Record<string, unknown> | undefined => {
  switch (form.kind) {
    case "nats":
      return {
        url: form.natsUrl.trim(),
        subject: form.natsSubject.trim(),
        jetstream: form.natsJetstream,
        ...(secretOrKeep(form.natsCreds, form.natsCredsSet)
          ? { creds: secretOrKeep(form.natsCreds, form.natsCredsSet) }
          : {}),
      };
    case "kafka":
      return {
        brokers: form.kafkaBrokers
          .split(",")
          .map((b) => b.trim())
          .filter(Boolean),
        topic: form.kafkaTopic.trim(),
        tls: form.kafkaTls,
        saslMechanism: form.kafkaSaslMechanism || undefined,
        saslUsername: form.kafkaSaslUsername.trim() || undefined,
        ...(secretOrKeep(form.kafkaSaslPassword, form.kafkaSaslPasswordSet)
          ? { saslPassword: secretOrKeep(form.kafkaSaslPassword, form.kafkaSaslPasswordSet) }
          : {}),
      };
    case "sqs":
      return {
        queueUrl: form.sqsQueueUrl.trim(),
        region: form.sqsRegion.trim(),
        endpoint: form.sqsEndpoint.trim() || undefined,
        accessKeyId: form.sqsAccessKeyId.trim() || undefined,
        ...(secretOrKeep(form.sqsSecretAccessKey, form.sqsSecretSet)
          ? { secretAccessKey: secretOrKeep(form.sqsSecretAccessKey, form.sqsSecretSet) }
          : {}),
      };
    case "sns":
      return {
        topicArn: form.snsTopicArn.trim(),
        region: form.snsRegion.trim(),
        endpoint: form.snsEndpoint.trim() || undefined,
        accessKeyId: form.snsAccessKeyId.trim() || undefined,
        ...(secretOrKeep(form.snsSecretAccessKey, form.snsSecretSet)
          ? { secretAccessKey: secretOrKeep(form.snsSecretAccessKey, form.snsSecretSet) }
          : {}),
      };
    case "pubsub":
      return {
        project: form.pubsubProject.trim(),
        topic: form.pubsubTopic.trim(),
        emulatorHost: form.pubsubEmulatorHost.trim() || undefined,
        ...(secretOrKeep(form.pubsubCredentialsJSON, form.pubsubCredsSet)
          ? { credentialsJSON: secretOrKeep(form.pubsubCredentialsJSON, form.pubsubCredsSet) }
          : {}),
      };
    default:
      return undefined;
  }
};

const buildPayload = (): EndpointCreate => {
  const headerNames = normalizePayloadHeaders(form.payloadHeaders);
  const headers: WebhookHeader[] = form.headers
    .filter((h) => h.name.trim())
    .map((h) => ({
      name: h.name.trim(),
      value: h.value,
      secret: h.secret,
    }));
  const body: EndpointCreate = {
    name: form.name.trim(),
    kind: form.kind,
    enabled: form.enabled,
    events: form.events,
    payload: {
      text: form.text,
      headers: headerNames.length ? headerNames : null,
      attachments: form.attachments,
      webSafeHtml: form.webSafeHtml,
    },
    security: { signatureMode: form.signatureMode },
    retry: {
      maxAttempts: Number(form.maxAttempts),
      backoffBase: form.backoffBase.trim(),
      backoffJitter: Number(form.backoffJitter),
      timeout: form.timeout.trim(),
    },
    delivery: {
      ordering: form.ordering,
      maxConcurrency: Number(form.maxConcurrency),
      contentType: form.contentType,
      retryOn4xx: form.retryOn4xx,
      autoDisableAfter: form.autoDisableAfter.trim(),
    },
    headers: headers.length ? headers : null,
  };
  if (form.kind === "http") {
    body.url = form.url.trim() || undefined;
    if (mtlsEnabled.value && (form.tlsClientCert.trim() || form.tlsClientKeySet)) {
      body.security = {
        signatureMode: form.signatureMode,
        tls: {
          clientCert: form.tlsClientCert.trim() || undefined,
          clientKey: secretOrKeep(form.tlsClientKey, form.tlsClientKeySet),
          caBundle: form.tlsCaBundle.trim() || undefined,
        },
      };
    }
  } else {
    body.sinkConfig = buildSinkConfig();
  }
  return body;
};

const onSubmit = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  const body = buildPayload();
  if (props.endpoint) {
    const { kind: _kind, ...rest } = body;
    emit("submit", { id: props.endpoint.id, ...rest } as EndpointUpdate & { id: string });
  } else {
    emit("submit", body);
  }
};

const close = (): void => {
  emit("update:modelValue", false);
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="isEdit ? t('webhooks.form_title_edit') : t('webhooks.form_title_create')"
    :max-width="720"
    :loading="submitting"
    fullscreen-on-mobile
    @update:model-value="emit('update:modelValue', $event)"
  >
    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-4"
    >
      {{ errorMsg }}
    </v-alert>
    <v-form
      ref="formRef"
      @submit.prevent="onSubmit"
    >
      <v-row>
        <v-col
          cols="12"
          md="8"
        >
          <v-text-field
            v-model="form.name"
            :label="t('webhooks.field_name')"
            :rules="required"
            :error-messages="fieldErrors.name ? [fieldErrors.name] : []"
            autocomplete="off"
          />
        </v-col>
        <v-col
          cols="12"
          md="4"
        >
          <AutocompleteSearch
            v-model="form.kind"
            :items="kindItems"
            :label="t('webhooks.field_kind')"
            :disabled="isEdit"
            class="no-validation"
          />
        </v-col>
        <v-col
          v-if="isHTTP"
          cols="12"
        >
          <v-text-field
            v-model="form.url"
            :label="t('webhooks.field_url')"
            :error-messages="fieldErrors.url ? [fieldErrors.url] : []"
            :rules="required"
            autocomplete="off"
          />
        </v-col>
        <v-col cols="12">
          <AutocompleteSearch
            v-model="form.events"
            :items="eventItems"
            :label="t('webhooks.field_events')"
            :rules="required"
            :hint="t('webhooks.field_events_hint')"
            persistent-hint
            multiple
            chips
            closable-chips
          />
        </v-col>
        <v-col cols="12">
          <v-switch
            v-model="form.enabled"
            :label="t('webhooks.enabled')"
            color="success"
            class="no-validation"
            hide-details
          />
        </v-col>
      </v-row>
      <RuleSummary
        :endpoint-id="endpoint?.id ?? null"
        :rule="endpointRule"
        class="mt-4"
      />

      <template v-if="form.kind === 'nats'">
        <div class="text-subtitle-2 mt-4 mb-2">
          {{ t("webhooks.sink_nats") }}
        </div>
        <v-row>
          <v-col cols="12">
            <v-text-field
              v-model="form.natsUrl"
              :label="t('webhooks.nats_url')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="8"
          >
            <v-text-field
              v-model="form.natsSubject"
              :label="t('webhooks.nats_subject')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
            class="d-flex align-center"
          >
            <v-switch
              v-model="form.natsJetstream"
              :label="t('webhooks.nats_jetstream')"
              color="secondary"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col cols="12">
            <v-textarea
              v-model="form.natsCreds"
              :label="t('webhooks.nats_creds')"
              :hint="secretHint(form.natsCredsSet)"
              :placeholder="form.natsCredsSet ? t('webhooks.secret_replace') : undefined"
              class="no-validation"
              rows="3"
              autocomplete="off"
              persistent-hint
            />
          </v-col>
        </v-row>
      </template>

      <template v-else-if="form.kind === 'kafka'">
        <div class="text-subtitle-2 mt-4 mb-2">
          {{ t("webhooks.sink_kafka") }}
        </div>
        <v-row>
          <v-col cols="12">
            <v-text-field
              v-model="form.kafkaBrokers"
              :label="t('webhooks.kafka_brokers')"
              :hint="t('webhooks.kafka_brokers_hint')"
              :rules="required"
              autocomplete="off"
              persistent-hint
            />
          </v-col>
          <v-col
            cols="12"
            md="8"
          >
            <v-text-field
              v-model="form.kafkaTopic"
              :label="t('webhooks.kafka_topic')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
            class="d-flex align-center"
          >
            <v-switch
              v-model="form.kafkaTls"
              :label="t('webhooks.kafka_tls')"
              color="secondary"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-select
              v-model="form.kafkaSaslMechanism"
              :items="saslItems"
              :label="t('webhooks.kafka_sasl_mechanism')"
              class="no-validation"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-text-field
              v-model="form.kafkaSaslUsername"
              :label="t('webhooks.kafka_sasl_username')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-text-field
              v-model="form.kafkaSaslPassword"
              :label="t('webhooks.kafka_sasl_password')"
              :hint="secretHint(form.kafkaSaslPasswordSet)"
              :placeholder="form.kafkaSaslPasswordSet ? t('webhooks.secret_replace') : undefined"
              class="no-validation"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
        </v-row>
      </template>

      <template v-else-if="form.kind === 'sqs'">
        <div class="text-subtitle-2 mt-4 mb-2">
          {{ t("webhooks.sink_sqs") }}
        </div>
        <v-row>
          <v-col cols="12">
            <v-text-field
              v-model="form.sqsQueueUrl"
              :label="t('webhooks.sqs_queue_url')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-text-field
              v-model="form.sqsRegion"
              :label="t('webhooks.aws_region')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="8"
          >
            <v-text-field
              v-model="form.sqsEndpoint"
              :label="t('webhooks.aws_endpoint')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.sqsAccessKeyId"
              :label="t('webhooks.aws_access_key')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.sqsSecretAccessKey"
              :label="t('webhooks.aws_secret_key')"
              :hint="secretHint(form.sqsSecretSet)"
              :placeholder="form.sqsSecretSet ? t('webhooks.secret_replace') : undefined"
              class="no-validation"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
        </v-row>
      </template>

      <template v-else-if="form.kind === 'sns'">
        <div class="text-subtitle-2 mt-4 mb-2">
          {{ t("webhooks.sink_sns") }}
        </div>
        <v-row>
          <v-col cols="12">
            <v-text-field
              v-model="form.snsTopicArn"
              :label="t('webhooks.sns_topic_arn')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-text-field
              v-model="form.snsRegion"
              :label="t('webhooks.aws_region')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="8"
          >
            <v-text-field
              v-model="form.snsEndpoint"
              :label="t('webhooks.aws_endpoint')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.snsAccessKeyId"
              :label="t('webhooks.aws_access_key')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.snsSecretAccessKey"
              :label="t('webhooks.aws_secret_key')"
              :hint="secretHint(form.snsSecretSet)"
              :placeholder="form.snsSecretSet ? t('webhooks.secret_replace') : undefined"
              class="no-validation"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
        </v-row>
      </template>

      <template v-else-if="form.kind === 'pubsub'">
        <div class="text-subtitle-2 mt-4 mb-2">
          {{ t("webhooks.sink_pubsub") }}
        </div>
        <v-row>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.pubsubProject"
              :label="t('webhooks.pubsub_project')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.pubsubTopic"
              :label="t('webhooks.pubsub_topic')"
              :rules="required"
              autocomplete="off"
            />
          </v-col>
          <v-col cols="12">
            <v-text-field
              v-model="form.pubsubEmulatorHost"
              :label="t('webhooks.pubsub_emulator')"
              class="no-validation"
              autocomplete="off"
            />
          </v-col>
          <v-col cols="12">
            <v-textarea
              v-model="form.pubsubCredentialsJSON"
              :label="t('webhooks.pubsub_credentials')"
              :hint="secretHint(form.pubsubCredsSet)"
              :placeholder="form.pubsubCredsSet ? t('webhooks.secret_replace') : undefined"
              class="no-validation"
              rows="4"
              autocomplete="off"
              persistent-hint
            />
          </v-col>
        </v-row>
      </template>

      <div class="text-subtitle-2 mt-4 mb-2">
        {{ t("webhooks.payload") }}
      </div>
      <v-row>
        <v-col
          cols="12"
          md="6"
        >
          <v-select
            v-model="form.text"
            :items="textItems"
            :label="t('webhooks.payload_text')"
            class="no-validation"
          />
        </v-col>
        <v-col
          cols="12"
          md="6"
        >
          <v-combobox
            :model-value="form.payloadHeaders"
            :label="t('webhooks.payload_headers')"
            :hint="t('webhooks.payload_headers_hint')"
            class="no-validation"
            multiple
            chips
            closable-chips
            persistent-hint
            @update:model-value="onPayloadHeaders"
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
        >
          <v-switch
            v-model="form.attachments"
            :label="t('webhooks.payload_attachments')"
            color="secondary"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
        >
          <v-switch
            v-model="form.webSafeHtml"
            :label="t('webhooks.payload_web_safe')"
            color="secondary"
            class="no-validation"
            hide-details
          />
        </v-col>
      </v-row>

      <div class="text-subtitle-2 mt-4 mb-2">
        {{ t("webhooks.security") }}
      </div>
      <v-row>
        <v-col
          cols="12"
          md="6"
        >
          <v-select
            v-model="form.signatureMode"
            :items="modeItems"
            :label="t('webhooks.security_mode')"
            class="no-validation"
          />
        </v-col>
      </v-row>

      <template v-if="isHTTP && mtlsEnabled">
        <div class="d-flex align-center justify-space-between mt-2 mb-2">
          <div class="text-subtitle-2">
            {{ t("webhooks.mtls") }}
          </div>
          <v-btn-inline
            :prepend-icon="mtlsOpen ? mdiChevronUp : mdiChevronDown"
            data-testid="webhooks-mtls-toggle"
            @click="mtlsOpen = !mtlsOpen"
          >
            {{ mtlsOpen ? t("webhooks.delivery_collapse") : t("webhooks.delivery_expand") }}
          </v-btn-inline>
        </div>
        <v-expand-transition>
          <v-row
            v-show="mtlsOpen"
          >
            <v-col cols="12">
              <v-textarea
                v-model="form.tlsClientCert"
                :label="t('webhooks.mtls_cert')"
                class="no-validation"
                rows="3"
                autocomplete="off"
              />
            </v-col>
            <v-col cols="12">
              <v-textarea
                v-model="form.tlsClientKey"
                :label="t('webhooks.mtls_key')"
                :hint="secretHint(form.tlsClientKeySet)"
                :placeholder="form.tlsClientKeySet ? t('webhooks.secret_replace') : undefined"
                class="no-validation"
                rows="3"
                autocomplete="off"
                persistent-hint
              />
            </v-col>
            <v-col cols="12">
              <v-textarea
                v-model="form.tlsCaBundle"
                :label="t('webhooks.mtls_ca')"
                class="no-validation"
                rows="3"
                autocomplete="off"
              />
            </v-col>
          </v-row>
        </v-expand-transition>
      </template>

      <div class="text-subtitle-2 mt-4 mb-2">
        {{ t("webhooks.retry") }}
      </div>
      <v-row>
        <v-col
          cols="6"
          md="3"
        >
          <v-text-field
            v-model.number="form.maxAttempts"
            :label="t('webhooks.retry_max')"
            :rules="[...required, ...positiveInteger]"
            type="number"
          />
        </v-col>
        <v-col
          cols="6"
          md="3"
        >
          <v-text-field
            v-model="form.backoffBase"
            :label="t('webhooks.retry_base')"
            :rules="required"
          />
        </v-col>
        <v-col
          cols="6"
          md="3"
        >
          <v-text-field
            v-model.number="form.backoffJitter"
            :label="t('webhooks.retry_jitter')"
            :rules="required"
            type="number"
            step="0.05"
          />
        </v-col>
        <v-col
          cols="6"
          md="3"
        >
          <v-text-field
            v-model="form.timeout"
            :label="t('webhooks.retry_timeout')"
            :rules="required"
          />
        </v-col>
      </v-row>

      <div class="d-flex align-center justify-space-between mt-4 mb-2">
        <div class="text-subtitle-2">
          {{ t("webhooks.delivery") }}
        </div>
        <v-btn-inline
          :prepend-icon="deliveryOpen ? mdiChevronUp : mdiChevronDown"
          data-testid="webhooks-delivery-toggle"
          @click="deliveryOpen = !deliveryOpen"
        >
          {{ deliveryOpen ? t("webhooks.delivery_collapse") : t("webhooks.delivery_expand") }}
        </v-btn-inline>
      </div>
      <v-expand-transition>
        <v-row
          v-show="deliveryOpen"
        >
          <v-col
            cols="12"
            sm="6"
          >
            <v-select
              v-model="form.ordering"
              :items="orderingItems"
              :label="t('webhooks.delivery_ordering')"
              class="no-validation"
            />
          </v-col>
          <v-col
            cols="12"
            sm="6"
          >
            <v-text-field
              v-model.number="form.maxConcurrency"
              :label="t('webhooks.delivery_concurrency')"
              :rules="[...required, ...positiveInteger]"
              type="number"
              min="1"
              max="64"
            />
          </v-col>
          <v-col
            cols="12"
            sm="6"
          >
            <v-select
              v-model="form.contentType"
              :items="contentTypeItems"
              :label="t('webhooks.delivery_content')"
              class="no-validation"
            />
          </v-col>
          <v-col
            cols="12"
            sm="6"
          >
            <v-text-field
              v-model="form.autoDisableAfter"
              :label="t('webhooks.delivery_auto_disable')"
              :rules="required"
            />
          </v-col>
          <v-col cols="12">
            <v-switch
              v-model="form.retryOn4xx"
              :label="t('webhooks.delivery_retry_4xx')"
              color="secondary"
              class="no-validation"
              hide-details
            />
          </v-col>
        </v-row>
      </v-expand-transition>

      <div
        v-if="isHTTP"
        class="d-flex align-center justify-space-between mt-4 mb-2"
      >
        <div class="text-subtitle-2">
          {{ t("webhooks.headers") }}
        </div>
        <v-btn-inline
          :color="createAction.color"
          :prepend-icon="mdiPlus"
          @click="addHeader"
        >
          {{ t("webhooks.header_add") }}
        </v-btn-inline>
      </div>
      <v-row
        v-for="(h, idx) in form.headers"
        v-show="isHTTP"
        :key="idx"
        class="mb-1"
      >
        <v-col
          cols="12"
          sm="4"
        >
          <v-text-field
            v-model="h.name"
            :label="t('webhooks.header_name')"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="5"
        >
          <v-text-field
            v-model="h.value"
            :label="t('webhooks.header_value')"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col
          cols="8"
          sm="2"
          class="d-flex align-center"
        >
          <v-checkbox
            v-model="h.secret"
            :label="t('webhooks.header_secret')"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col
          cols="4"
          sm="1"
          class="d-flex align-center justify-end"
        >
          <v-btn-inline
            :color="deleteAction.color"
            :icon="mdiDelete"
            @click="removeHeader(idx)"
          />
        </v-col>
      </v-row>
    </v-form>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("webhooks.cancel") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="submitting"
        @click="onSubmit"
      >
        {{ t("webhooks.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
