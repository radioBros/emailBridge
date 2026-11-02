<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type {
  GatewayCreate,
  GatewayCredentialsInput,
  GatewayUpdate,
  GatewayView,
} from "@/api/gateways";
import { actionOf } from "@/constants/Actions";
import { required, positiveInteger } from "@/constants/Validations";
import {
  isAPIKind,
  KIND_OPTIONS,
  POLICY_OPTIONS,
  TLS_OPTIONS,
} from "@/features/gateways/helpers/labels";

const props = defineProps<{
  modelValue: boolean;
  gateway: GatewayView | null;
  submitting: boolean;
  errorMsg: string | null;
  fieldErrors: Record<string, string>;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: GatewayCreate | (GatewayUpdate & { id: string })];
}>();

const { t } = useI18n();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const saveAction = actionOf("save");
const createAction = actionOf("create");

const form = reactive({
  name: "",
  kind: "smtp" as string,
  host: "",
  port: 465,
  tls: "implicit" as string,
  user: "",
  pass: "",
  region: "",
  domain: "",
  baseUrl: "",
  from: "",
  accessKey: "",
  secretKey: "",
  apiKey: "",
  webhookSecret: "",
  ehloName: "",
  localAddress: "",
  proxy: "",
  policy: "standard" as string,
  ignoreCertErrors: false,
  enabled: true,
});

const isEdit = computed(() => Boolean(props.gateway));
const title = computed(() =>
  isEdit.value ? t("gateways.form_title_edit") : t("gateways.form_title_create"),
);
const apiMode = computed(() => isAPIKind(form.kind));

const kindItems = computed(() =>
  KIND_OPTIONS.map((v) => ({ title: t(`gateways.kind_${v}`), value: v })),
);

const tlsItems = computed(() =>
  TLS_OPTIONS.map((v) => ({ title: t(`gateways.tls_${v}`), value: v })),
);

const policyItems = computed(() =>
  POLICY_OPTIONS.map((v) => ({
    title: t(v === "atMostOnce" ? "gateways.policy_atMostOnce" : "gateways.policy_standard"),
    value: v,
  })),
);

const webhookUrl = computed(() => props.gateway?.webhookUrl ?? "");

const reset = (): void => {
  const g = props.gateway;
  form.name = g?.name ?? "";
  form.kind = g?.kind || "smtp";
  form.host = g?.host ?? "";
  form.port = g?.port ?? 465;
  form.tls = g?.tls ?? "implicit";
  form.user = g?.auth?.user ?? "";
  form.pass = "";
  form.region = g?.api?.region ?? "";
  form.domain = g?.api?.domain ?? "";
  form.baseUrl = g?.api?.baseUrl ?? "";
  form.from = g?.api?.from ?? "";
  form.accessKey = "";
  form.secretKey = "";
  form.apiKey = "";
  form.webhookSecret = "";
  form.ehloName = g?.ehloName ?? "";
  form.localAddress = g?.localAddress ?? "";
  form.proxy = g?.proxy ?? "";
  form.policy = g?.policy || "standard";
  form.ignoreCertErrors = Boolean(g?.ignoreCertErrors);
  form.enabled = g?.enabled ?? true;
};

watch(
  () => props.modelValue,
  (open) => {
    if (open) reset();
  },
);

const close = (): void => emit("update:modelValue", false);

const credsPayload = (): GatewayCredentialsInput | undefined => {
  const c: GatewayCredentialsInput = {};
  if (form.accessKey) c.accessKey = form.accessKey;
  if (form.secretKey) c.secretKey = form.secretKey;
  if (form.apiKey) c.apiKey = form.apiKey;
  if (form.webhookSecret) c.webhookSecret = form.webhookSecret;
  return Object.keys(c).length ? c : undefined;
};

const onSubmit = async (): Promise<void> => {
  const valid = (await formRef.value?.validate())?.valid ?? true;
  if (!valid) return;
  if (isEdit.value && props.gateway) {
    if (apiMode.value) {
      const body: GatewayUpdate & { id: string } = {
        id: props.gateway.id,
        name: form.name.trim(),
        api: {
          region: form.region.trim() || undefined,
          domain: form.domain.trim() || undefined,
          baseUrl: form.baseUrl.trim() || undefined,
          from: form.from.trim() || undefined,
        },
        credentials: credsPayload(),
        policy: form.policy,
        enabled: form.enabled,
      };
      emit("submit", body);
      return;
    }
    const body: GatewayUpdate & { id: string } = {
      id: props.gateway.id,
      name: form.name.trim(),
      host: form.host.trim(),
      port: Number(form.port),
      tls: form.tls,
      auth: {
        user: form.user.trim(),
        ...(form.pass ? { pass: form.pass } : {}),
      },
      ehloName: form.ehloName.trim() || undefined,
      localAddress: form.localAddress.trim() || undefined,
      proxy: form.proxy.trim() || undefined,
      policy: form.policy,
      ignoreCertErrors: form.ignoreCertErrors,
      enabled: form.enabled,
    };
    emit("submit", body);
    return;
  }
  if (apiMode.value) {
    emit("submit", {
      name: form.name.trim(),
      kind: form.kind,
      api: {
        region: form.region.trim() || undefined,
        domain: form.domain.trim() || undefined,
        baseUrl: form.baseUrl.trim() || undefined,
        from: form.from.trim() || undefined,
      },
      credentials: {
        accessKey: form.accessKey || undefined,
        secretKey: form.secretKey || undefined,
        apiKey: form.apiKey || undefined,
        webhookSecret: form.webhookSecret || undefined,
      },
      policy: form.policy,
      enabled: form.enabled,
    } satisfies GatewayCreate);
    return;
  }
  emit("submit", {
    name: form.name.trim(),
    kind: "smtp",
    host: form.host.trim(),
    port: Number(form.port),
    tls: form.tls,
    auth: { user: form.user.trim(), pass: form.pass },
    ehloName: form.ehloName.trim() || undefined,
    localAddress: form.localAddress.trim() || undefined,
    proxy: form.proxy.trim() || undefined,
    policy: form.policy,
    ignoreCertErrors: form.ignoreCertErrors,
    enabled: form.enabled,
  } satisfies GatewayCreate);
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="title"
    :max-width="720"
    :loading="submitting"
    persistent
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
        <v-col cols="12">
          <v-text-field
            v-model="form.name"
            :label="t('gateways.field_name')"
            :rules="required"
            :error-messages="fieldErrors.name ? [fieldErrors.name] : []"
            data-testid="gateway-name"
          />
        </v-col>
        <v-col cols="12">
          <AutocompleteSearch
            v-model="form.kind"
            :items="kindItems"
            :label="t('gateways.field_kind')"
            :rules="required"
            :disabled="isEdit"
            data-testid="gateway-kind"
          />
        </v-col>

        <template v-if="!apiMode">
          <v-col
            cols="12"
            md="8"
          >
            <v-text-field
              v-model="form.host"
              :label="t('gateways.field_host')"
              :rules="required"
              :error-messages="fieldErrors.host ? [fieldErrors.host] : []"
            />
          </v-col>
          <v-col
            cols="12"
            md="4"
          >
            <v-text-field
              v-model.number="form.port"
              :label="t('gateways.field_port')"
              :rules="[...required, ...positiveInteger]"
              type="number"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-select
              v-model="form.tls"
              :items="tlsItems"
              :label="t('gateways.field_tls')"
              :rules="required"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-select
              v-model="form.policy"
              :items="policyItems"
              :label="t('gateways.field_policy')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.user"
              :label="t('gateways.field_user')"
              :rules="required"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.pass"
              :label="t('gateways.field_pass')"
              :hint="isEdit ? t('gateways.field_pass_hint') : undefined"
              :rules="isEdit ? [] : required"
              :class="{ 'no-validation': isEdit }"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.ehloName"
              :label="t('gateways.field_ehlo')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.localAddress"
              :label="t('gateways.field_local')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col cols="12">
            <v-text-field
              v-model="form.proxy"
              :label="t('gateways.field_proxy')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-switch
              v-model="form.ignoreCertErrors"
              :label="t('gateways.field_ignore_cert')"
              class="no-validation"
              color="warning"
              hide-details
            />
          </v-col>
        </template>

        <template v-else>
          <v-col
            v-if="form.kind === 'ses'"
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.region"
              :label="t('gateways.field_region')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col
            v-if="form.kind === 'mailgun'"
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.domain"
              :label="t('gateways.field_domain')"
              :rules="required"
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.from"
              :label="t('gateways.field_from')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col cols="12">
            <v-text-field
              v-model="form.baseUrl"
              :label="t('gateways.field_base_url')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <template v-if="form.kind === 'ses'">
            <v-col
              cols="12"
              md="6"
            >
              <v-text-field
                v-model="form.accessKey"
                :label="t('gateways.field_access_key')"
                :hint="isEdit ? t('gateways.field_secret_hint') : undefined"
                :rules="isEdit ? [] : required"
                :class="{ 'no-validation': isEdit }"
                type="password"
                autocomplete="new-password"
                persistent-hint
              />
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <v-text-field
                v-model="form.secretKey"
                :label="t('gateways.field_secret_key')"
                :hint="isEdit ? t('gateways.field_secret_hint') : undefined"
                :rules="isEdit ? [] : required"
                :class="{ 'no-validation': isEdit }"
                type="password"
                autocomplete="new-password"
                persistent-hint
              />
            </v-col>
          </template>
          <v-col
            v-else
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.apiKey"
              :label="t('gateways.field_api_key')"
              :hint="isEdit ? t('gateways.field_secret_hint') : undefined"
              :rules="isEdit ? [] : required"
              :class="{ 'no-validation': isEdit }"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-text-field
              v-model="form.webhookSecret"
              :label="t('gateways.field_webhook_secret')"
              :hint="isEdit ? t('gateways.field_secret_hint') : undefined"
              class="no-validation"
              type="password"
              autocomplete="new-password"
              persistent-hint
            />
          </v-col>
          <v-col
            cols="12"
            md="6"
          >
            <v-select
              v-model="form.policy"
              :items="policyItems"
              :label="t('gateways.field_policy')"
              class="no-validation"
              hide-details
            />
          </v-col>
          <v-col cols="12">
            <v-text-field
              v-if="webhookUrl"
              :model-value="webhookUrl"
              :label="t('gateways.field_webhook_url')"
              :hint="t('gateways.webhook_url_hint')"
              class="no-validation"
              data-testid="gateway-webhook-url"
              readonly
              persistent-hint
            />
            <v-alert
              v-else
              type="info"
            >
              {{ t("gateways.webhook_url_after_create") }}
            </v-alert>
          </v-col>
        </template>

        <v-col
          cols="12"
          md="6"
        >
          <v-switch
            v-model="form.enabled"
            :label="t('gateways.field_enabled')"
            class="no-validation"
            color="success"
            hide-details
          />
        </v-col>
      </v-row>
    </v-form>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("gateways.cancel") }}
      </v-btn>
      <v-btn
        :color="isEdit ? saveAction.color : createAction.color"
        :prepend-icon="isEdit ? saveAction.icon : createAction.icon"
        :loading="submitting"
        data-testid="gateway-save"
        @click="onSubmit"
      >
        {{ t("gateways.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
