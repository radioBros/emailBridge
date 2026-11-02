<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiBrain,
  mdiCheckCircle,
  mdiLanConnect,
  mdiShieldLock,
  mdiVectorPolyline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ApiProblemError } from "@/api/client";
import {
  getAISettings,
  listAIModels,
  putAISettings,
  type AIFeature,
  type AIModel,
  type AIProvider,
  type AISettings,
} from "@/api/ai";
import {
  AI_PROVIDERS,
  DEFAULT_BASE_URLS,
  hasEmbeddingsApi,
  modelItems,
} from "@/features/ai/helpers/providers";

const { t } = useI18n();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
const saveAction = actionOf("save");
const connectAction = actionOf("reconnect");

const loadError = ref<string | null>(null);
const forbidden = ref(false);
const saving = ref(false);
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);

/** What the server has, to restore a provider's base URL / model when the picker returns to it. */
const stored = ref<AISettings | null>(null);
const provider = ref<AIProvider>("openai");
const baseUrl = ref("");
const model = ref("");
const apiKey = ref("");
const clearKey = ref(false);
const features = ref<AIFeature[]>(["summary", "classification", "extraction", "risk"]);
const piiRedact = ref(true);
const bodyMaxChars = ref(8000);

/** Connect: the provider's models, or why the provider refused. */
const models = ref<AIModel[]>([]);
const connecting = ref(false);
const connectError = ref("");
const connected = ref(false);
const manualModel = ref(false);

/** Embeddings endpoint of semantic search (G1); empty base URL = reuse the main provider. */
const embBaseUrl = ref("");
const embModel = ref("");
const embApiKey = ref("");
const embClearKey = ref(false);
const embApiKeySet = ref(false);
const embEffectiveModel = ref("");
const embProblem = ref("");
const embModels = ref<AIModel[]>([]);
const embConnecting = ref(false);
const embConnectError = ref("");

const isNone = computed(() => provider.value === "none");
const isCustom = computed(() => provider.value === "local");
const keySet = computed(() => (stored.value?.providerKeysSet ?? []).includes(provider.value)
  || (stored.value?.provider === provider.value && Boolean(stored.value?.apiKeySet)));

/** Why the embeddings endpoint is not usable, as an i18n key (the server sends an English sentence). */
const embProblemKey = (s: AISettings): string => {
  if (s.embeddings?.baseUrl) return "ai.emb_problem_model";
  if (s.provider === "anthropic" || s.provider === "xai" || s.provider === "none") {
    return "ai.emb_problem_no_api";
  }
  if (s.provider === "local") return s.baseUrl ? "ai.emb_problem_model" : "ai.emb_problem_local";
  if (s.provider === "gemini" && !s.embeddings?.model) return "ai.emb_problem_model";
  return "ai.emb_problem_key";
};

/** An own base URL needs a model; a provider without embeddings API needs an own base URL. */
const embModelRules = computed(() => (embBaseUrl.value.trim() ? required : []));
const embBaseUrlRules = computed(() =>
  !hasEmbeddingsApi(provider.value) && embModel.value.trim() ? required : [],
);
const baseUrlHint = computed(() =>
  isCustom.value
    ? t("ai.field_base_url_hint_custom")
    : t("ai.field_base_url_hint", { url: DEFAULT_BASE_URLS[provider.value] ?? "" }),
);

const providerItems = computed(() =>
  AI_PROVIDERS.map((value) => ({ title: t(`ai.provider_${value}`), value })),
);

const featureItems = computed(() => [
  { title: t("ai.feature_summary"), value: "summary" as const },
  { title: t("ai.feature_classification"), value: "classification" as const },
  { title: t("ai.feature_extraction"), value: "extraction" as const },
  { title: t("ai.feature_risk"), value: "risk" as const },
]);

const modelOptions = computed(() => modelItems(models.value, model.value));
const embModelOptions = computed(() => modelItems(embModels.value, embModel.value));

/** Embedding models come from the own endpoint, else from the main provider when it can embed. */
const canLoadEmbModels = computed(
  () => Boolean(embBaseUrl.value.trim()) || hasEmbeddingsApi(provider.value),
);

const resetConnection = (): void => {
  models.value = [];
  connected.value = false;
  connectError.value = "";
};

/** A new provider starts from its own saved values; a base URL or model never carries across. */
watch(provider, (next, prev) => {
  if (prev === undefined || next === prev) return;
  resetConnection();
  apiKey.value = "";
  clearKey.value = false;
  manualModel.value = false;
  const s = stored.value;
  baseUrl.value = s?.provider === next ? (s.baseUrl ?? "") : "";
  model.value = s?.provider === next ? (s.model ?? "") : "";
});

watch([baseUrl, apiKey], resetConnection);

const apply = (s: AISettings): void => {
  stored.value = s;
  provider.value = s.provider;
  baseUrl.value = s.baseUrl ?? "";
  model.value = s.model ?? "";
  apiKey.value = "";
  clearKey.value = false;
  features.value = s.features?.length
    ? [...s.features]
    : ["summary", "classification", "extraction", "risk"];
  piiRedact.value = s.piiRedact;
  bodyMaxChars.value = s.bodyMaxChars || 8000;
  embBaseUrl.value = s.embeddings?.baseUrl ?? "";
  embModel.value = s.embeddings?.model ?? "";
  embApiKey.value = "";
  embClearKey.value = false;
  embApiKeySet.value = s.embeddings?.apiKeySet ?? false;
  embEffectiveModel.value = s.embeddings?.configured ? (s.embeddings.effectiveModel ?? "") : "";
  embProblem.value = s.embeddings?.configured ? "" : embProblemKey(s);
};

const load = async (): Promise<void> => {
  start();
  loadError.value = null;
  forbidden.value = false;
  try {
    apply(await getAISettings());
    resetConnection();
    finish(true);
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 403) {
      forbidden.value = true;
    } else {
      loadError.value = messageOf(err);
    }
    finish(false);
  }
};

onMounted(() => {
  void load();
});

const connect = async (): Promise<void> => {
  if (isNone.value) return;
  connecting.value = true;
  connectError.value = "";
  try {
    const list = await listAIModels({
      provider: provider.value as Exclude<AIProvider, "none">,
      baseUrl: baseUrl.value.trim() || undefined,
      apiKey: apiKey.value.trim() || undefined,
    });
    models.value = list;
    connected.value = true;
    manualModel.value = false;
    if (!model.value && list.length) model.value = list[0].id;
  } catch (err) {
    connected.value = false;
    connectError.value = messageOf(err);
  } finally {
    connecting.value = false;
  }
};

const connectEmbeddings = async (): Promise<void> => {
  embConnecting.value = true;
  embConnectError.value = "";
  try {
    const own = embBaseUrl.value.trim();
    embModels.value = await listAIModels(
      own
        ? { provider: "local", baseUrl: own, apiKey: embApiKey.value.trim() || undefined, kind: "embeddings" }
        : {
            provider: provider.value as Exclude<AIProvider, "none">,
            baseUrl: baseUrl.value.trim() || undefined,
            apiKey: apiKey.value.trim() || undefined,
            kind: "embeddings",
          },
    );
  } catch (err) {
    embModels.value = [];
    embConnectError.value = messageOf(err);
  } finally {
    embConnecting.value = false;
  }
};

const save = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  saving.value = true;
  try {
    const body: Parameters<typeof putAISettings>[0] = {
      provider: provider.value,
      model: isNone.value ? undefined : model.value.trim(),
      baseUrl: isNone.value ? undefined : baseUrl.value.trim() || undefined,
      features: features.value,
      piiRedact: piiRedact.value,
      bodyMaxChars: bodyMaxChars.value,
    };
    if (!isNone.value) {
      if (clearKey.value) {
        body.apiKey = "";
      } else if (apiKey.value.trim()) {
        body.apiKey = apiKey.value.trim();
      }
    }
    body.embeddings = { baseUrl: embBaseUrl.value.trim() || undefined, model: embModel.value.trim() || undefined };
    if (embClearKey.value) {
      body.embeddings.apiKey = "";
    } else if (embApiKey.value.trim()) {
      body.embeddings.apiKey = embApiKey.value.trim();
    }
    apply(await putAISettings(body));
    notify.push({ message: t("ai.saved"), color: "success" });
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    saving.value = false;
  }
};
</script>

<template>
  <div>
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      shape="form"
    />

    <AppCard
      v-else-if="forbidden"
      class="text-center py-8"
    >
      <v-avatar-hero
        color="warning"
        class="mb-4"
      >
        <v-icon
          :icon="mdiShieldLock"
        />
      </v-avatar-hero>
      <div class="text-title-medium mb-1">
        {{ t("ai.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("ai.no_permission_body") }}
      </p>
    </AppCard>

    <div
      v-else
      class="position-relative"
    >
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("ai.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <AppCard>
        <template #header>
          <div class="d-flex align-center ga-3">
            <v-avatar
              color="primary"
            >
              <v-icon
                :icon="mdiBrain"
              />
            </v-avatar>
            <div class="min-w-0">
              <div class="text-title-medium">
                {{ t("ai.title") }}
              </div>
              <p class="text-body-small text-medium-emphasis mb-0">
                {{ t("ai.subtitle") }}
              </p>
            </div>
          </div>
        </template>

        <v-alert
          type="info"
          class="mb-4"
        >
          {{ t("ai.gdpr_note") }}
        </v-alert>

        <v-form
          ref="formRef"
          @submit.prevent="save"
        >
          <v-row>
            <v-col
              cols="12"
              md="6"
            >
              <AutocompleteSearch
                v-model="provider"
                :items="providerItems"
                :label="t('ai.field_provider')"
                class="no-validation"
                data-testid="ai-provider"
              />
            </v-col>
            <v-col
              v-if="!isNone"
              cols="12"
              md="6"
            >
              <v-text-field
                v-model="baseUrl"
                :label="t('ai.field_base_url')"
                :hint="baseUrlHint"
                :rules="isCustom ? required : []"
                :class="isCustom ? undefined : 'no-validation'"
                data-testid="ai-base-url"
                persistent-hint
              />
            </v-col>
            <v-col
              v-if="isNone"
              cols="12"
              md="6"
            >
              <p
                class="text-body-medium text-medium-emphasis mb-0"
                data-testid="ai-none-hint"
              >
                {{ t("ai.none_hint") }}
              </p>
            </v-col>

            <template v-if="!isNone">
              <v-col cols="12">
                <div class="d-flex flex-column flex-md-row align-md-start ga-3">
                  <div class="flex-grow-1 min-w-0">
                    <v-text-field
                      v-model="apiKey"
                      :label="t('ai.field_api_key')"
                      :hint="keySet ? t('ai.field_api_key_set') : (isCustom ? t('ai.field_api_key_hint_custom') : t('ai.field_api_key_hint'))"
                      class="no-validation"
                      type="password"
                      autocomplete="new-password"
                      data-testid="ai-api-key"
                      persistent-hint
                    />
                    <v-checkbox
                      v-if="keySet"
                      v-model="clearKey"
                      :label="t('ai.field_api_key_clear')"
                      class="no-validation mt-1"
                      hide-details
                    />
                  </div>
                  <v-btn
                    :color="connectAction.color"
                    :prepend-icon="connectAction.icon"
                    :loading="connecting"
                    :disabled="connecting || (isCustom && !baseUrl.trim())"
                    class="mt-md-2"
                    data-testid="ai-connect"
                    @click="connect"
                  >
                    {{ t("ai.connect") }}
                  </v-btn>
                </div>
                <v-alert
                  v-if="connectError"
                  type="error"
                  class="mt-3"
                  data-testid="ai-connect-error"
                >
                  {{ connectError }}
                </v-alert>
                <div
                  v-else-if="connected"
                  class="d-flex align-center ga-2 mt-3 text-body-medium text-success"
                  data-testid="ai-connected"
                >
                  <v-icon
                    :icon="mdiCheckCircle"
                  />
                  {{ t("ai.connected", { n: models.length }) }}
                </div>
              </v-col>

              <v-col cols="12">
                <v-text-field
                  v-if="manualModel"
                  v-model="model"
                  :label="t('ai.field_model')"
                  :hint="t('ai.field_model_manual_hint')"
                  :rules="required"
                  data-testid="ai-model-manual"
                  persistent-hint
                />
                <AutocompleteSearch
                  v-else
                  v-model="model"
                  :items="modelOptions"
                  :label="t('ai.field_model')"
                  :hint="modelOptions.length ? undefined : t('ai.field_model_hint')"
                  :rules="required"
                  data-testid="ai-model"
                  persistent-hint
                />
                <v-btn
                  :color="actionOf('edit').color"
                  class="mt-2"
                  data-testid="ai-model-toggle"
                  @click="manualModel = !manualModel"
                >
                  {{ manualModel ? t("ai.model_pick") : t("ai.model_manual") }}
                </v-btn>
              </v-col>
            </template>

            <v-col cols="12">
              <v-select
                v-model="features"
                :items="featureItems"
                :label="t('ai.field_features')"
                :disabled="isNone"
                item-title="title"
                item-value="value"
                class="no-validation"
                multiple
                chips
              />
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <v-switch
                v-model="piiRedact"
                :label="t('ai.field_pii')"
                class="no-validation"
                color="primary"
                hide-details
              />
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <v-text-field
                v-model.number="bodyMaxChars"
                :label="t('ai.field_body_max')"
                class="no-validation"
                type="number"
                min="256"
                max="100000"
              />
            </v-col>

            <v-col cols="12">
              <v-divider class="mb-4" />
              <div class="d-flex align-start ga-3">
                <v-avatar
                  color="info"
                >
                  <v-icon
                    :icon="mdiVectorPolyline"
                  />
                </v-avatar>
                <div class="min-w-0">
                  <div class="text-title-medium mb-1">
                    {{ t("ai.emb_title") }}
                  </div>
                  <p class="text-body-medium text-medium-emphasis mb-0">
                    {{ t("ai.emb_hint") }}
                  </p>
                </div>
              </div>
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <v-text-field
                v-model="embBaseUrl"
                :label="t('ai.emb_base_url')"
                :hint="t('ai.emb_base_url_hint')"
                :rules="embBaseUrlRules"
                :class="embBaseUrlRules.length ? undefined : 'no-validation'"
                data-testid="ai-emb-base-url"
                persistent-hint
              />
            </v-col>
            <v-col
              cols="12"
              md="6"
            >
              <AutocompleteSearch
                v-if="embModels.length"
                v-model="embModel"
                :items="embModelOptions"
                :label="t('ai.emb_model')"
                :hint="t('ai.emb_model_hint')"
                class="no-validation"
                data-testid="ai-emb-model-pick"
                persistent-hint
              />
              <v-text-field
                v-else
                v-model="embModel"
                :label="t('ai.emb_model')"
                :hint="t('ai.emb_model_hint')"
                :rules="embModelRules"
                :class="embModelRules.length ? undefined : 'no-validation'"
                data-testid="ai-emb-model"
                persistent-hint
              />
            </v-col>
            <v-col cols="12">
              <div class="d-flex flex-column flex-md-row align-md-start ga-3">
                <div class="flex-grow-1 min-w-0">
                  <v-text-field
                    v-model="embApiKey"
                    :label="t('ai.emb_api_key')"
                    :hint="embApiKeySet ? t('ai.field_api_key_set') : t('ai.emb_api_key_hint')"
                    class="no-validation"
                    type="password"
                    autocomplete="new-password"
                    data-testid="ai-emb-api-key"
                    persistent-hint
                  />
                  <v-checkbox
                    v-if="embApiKeySet"
                    v-model="embClearKey"
                    :label="t('ai.field_api_key_clear')"
                    class="no-validation mt-1"
                    hide-details
                  />
                </div>
                <v-btn
                  v-if="canLoadEmbModels"
                  :color="connectAction.color"
                  :prepend-icon="mdiLanConnect"
                  :loading="embConnecting"
                  :disabled="embConnecting"
                  class="mt-md-2"
                  data-testid="ai-emb-connect"
                  @click="connectEmbeddings"
                >
                  {{ t("ai.emb_connect") }}
                </v-btn>
              </div>
              <v-alert
                v-if="embConnectError"
                type="error"
                class="mt-3"
              >
                {{ embConnectError }}
              </v-alert>
            </v-col>
            <v-col cols="12">
              <v-alert
                v-if="embProblem"
                type="warning"
                data-testid="ai-emb-problem"
              >
                {{ t(embProblem) }}
              </v-alert>
              <p
                v-else-if="embEffectiveModel"
                class="text-body-small text-medium-emphasis mb-0"
                data-testid="ai-emb-effective"
              >
                {{ t("ai.emb_effective", { model: embEffectiveModel }) }}
              </p>
            </v-col>
          </v-row>
        </v-form>

        <template #footer>
          <div class="d-flex justify-end w-100">
            <v-btn
              :color="saveAction.color"
              :prepend-icon="saveAction.icon"
              :loading="saving"
              :disabled="saving"
              data-testid="ai-save"
              @click="save"
            >
              {{ t("ai.save") }}
            </v-btn>
          </div>
        </template>
      </AppCard>

      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>
