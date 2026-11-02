<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import AppDialog from "@/components/AppDialog.vue";
import {
  searchAccounts,
  searchEndpoints,
  searchRoutes,
  type ApiKeyPatchBody,
} from "@/api/apikeys";
import type { ApiKeyRow } from "@/features/organization/composables/useApiKeys";
import {
  granularScope,
  MCP_SCOPE,
  scopeItems,
  type GranularResource,
} from "@/features/organization/helpers/scopes";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  apiKey: ApiKeyRow | null;
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [body: ApiKeyPatchBody];
}>();

const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const name = ref("");
const scopes = ref<string[]>([]);
const ipsText = ref("");
const referrersText = ref("");
const rateRps = ref<number | null>(null);
const rateBurst = ref<number | null>(null);
const granularResource = ref<GranularResource | null>(null);
const granularMode = ref<"read" | "write">("read");
const granularId = ref<string | null>(null);
const accountItems = ref<Array<{ title: string; value: string }>>([]);
const endpointItems = ref<Array<{ title: string; value: string }>>([]);
const routeItems = ref<Array<{ title: string; value: string }>>([]);
const resourceLoading = ref(false);
/** The MCP scope is labelled so it is easy to find among the resource scopes. */
const scopesList = computed(() =>
  scopeItems((s) => (s === MCP_SCOPE ? t("organization.api_keys.scope_mcp") : undefined)),
);
const resourceKindItems = computed(() =>
  (["accounts", "webhooks", "routes"] as GranularResource[]).map((value) => ({
    title: t(`organization.api_keys.resource_${value}`),
    value,
  })),
);
const modeItems = [
  { title: "read", value: "read" },
  { title: "write", value: "write" },
];

watch(
  () => [open.value, props.apiKey] as const,
  ([isOpen, key]) => {
    if (isOpen && key) {
      name.value = key.name;
      scopes.value = [...key.scopes];
      const rest = key.raw.restrictions;
      ipsText.value = (rest?.ips ?? []).join("\n");
      referrersText.value = (rest?.referrers ?? []).join("\n");
      rateRps.value = key.raw.rateLimit?.rps ?? null;
      rateBurst.value = key.raw.rateLimit?.burst ?? null;
      granularResource.value = null;
      granularId.value = null;
      void loadAccounts("");
    }
  },
);

watch(granularResource, () => {
  granularId.value = null;
  if (granularResource.value === "accounts") void loadAccounts("");
  if (granularResource.value === "webhooks") void loadEndpoints("");
  if (granularResource.value === "routes") void loadRoutes("");
});

const loadAccounts = async (q: string): Promise<void> => {
  resourceLoading.value = true;
  try {
    const res = await searchAccounts(q);
    accountItems.value = (res.data ?? []).map((a) => ({
      title: `${a.name} (${a.email})`,
      value: a.id,
    }));
  } catch (err) {
    accountItems.value = [];
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    resourceLoading.value = false;
  }
};

const loadEndpoints = async (q: string): Promise<void> => {
  resourceLoading.value = true;
  try {
    const res = await searchEndpoints(q);
    endpointItems.value = (res.data ?? []).map((e) => ({ title: e.name, value: e.id }));
  } catch (err) {
    endpointItems.value = [];
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    resourceLoading.value = false;
  }
};

const loadRoutes = async (q: string): Promise<void> => {
  resourceLoading.value = true;
  try {
    const res = await searchRoutes(q);
    routeItems.value = (res.data ?? []).map((r) => ({ title: r.name, value: r.id }));
  } catch (err) {
    routeItems.value = [];
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    resourceLoading.value = false;
  }
};

const resourceItems = computed(() => {
  if (granularResource.value === "webhooks") return endpointItems.value;
  if (granularResource.value === "routes") return routeItems.value;
  return accountItems.value;
});

const addGranular = (): void => {
  if (!granularResource.value || !granularId.value) return;
  const s = granularScope(granularResource.value, granularMode.value, granularId.value);
  if (!scopes.value.includes(s)) scopes.value = [...scopes.value, s];
  granularId.value = null;
};

const onResourceSearch = (q: string): void => {
  if (granularResource.value === "webhooks") void loadEndpoints(q);
  else if (granularResource.value === "routes") void loadRoutes(q);
  else void loadAccounts(q);
};

const lines = (text: string): string[] =>
  text
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

const onSubmit = (): void => {
  if (!name.value.trim() || !scopes.value.length) return;
  const body: ApiKeyPatchBody = {
    name: name.value.trim(),
    scopes: [...new Set(scopes.value)],
  };
  const ips = lines(ipsText.value);
  const referrers = lines(referrersText.value);
  body.restrictions = { ips, referrers };
  body.rateLimit = {
    rps: rateRps.value ?? undefined,
    burst: rateBurst.value ?? undefined,
  };
  emit("submit", body);
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organization.api_keys.edit_title')"
    :max-width="640"
    :loading="loading"
    fullscreen-on-mobile
  >
    <v-text-field
      v-model="name"
      :label="t('organization.api_keys.col_name')"
      :rules="required"
      class="mb-2"
    />
    <AutocompleteSearch
      v-model="scopes"
      :items="scopesList"
      :label="t('organization.api_keys.col_scopes')"
      :hint="t('organization.api_keys.scopes_hint')"
      :rules="required"
      class="mb-2"
      persistent-hint
      multiple
      chips
      closable-chips
    />

    <div class="text-label-large text-medium-emphasis mb-2">
      {{ t("organization.api_keys.granular_title") }}
    </div>
    <v-row>
      <v-col
        cols="12"
        md="4"
      >
        <AutocompleteSearch
          v-model="granularResource"
          :items="resourceKindItems"
          :label="t('organization.api_keys.granular_resource')"
          class="no-validation"
          clearable
        />
      </v-col>
      <v-col
        cols="12"
        md="3"
      >
        <AutocompleteSearch
          v-model="granularMode"
          :items="modeItems"
          :label="t('organization.api_keys.granular_mode')"
          class="no-validation"
        />
      </v-col>
      <v-col
        cols="12"
        md="5"
      >
        <div class="d-flex ga-2">
          <AutocompleteSearch
            v-model="granularId"
            :items="resourceItems"
            :loading="resourceLoading"
            :disabled="!granularResource"
            :label="t('organization.api_keys.granular_id')"
            class="no-validation flex-grow-1"
            clearable
            server-search
            @update:search="onResourceSearch"
          />
          <v-btn
            :disabled="!granularResource || !granularId"
            :color="actionOf('create').color"
            @click="addGranular"
          >
            {{ t("organization.api_keys.granular_add") }}
          </v-btn>
        </div>
      </v-col>
    </v-row>

    <v-textarea
      v-model="ipsText"
      :label="t('organization.api_keys.restrictions_ips')"
      class="mb-2 mt-2 no-validation"
      rows="2"
      auto-grow
    />
    <v-textarea
      v-model="referrersText"
      :label="t('organization.api_keys.restrictions_referrers')"
      class="mb-2 no-validation"
      rows="2"
      auto-grow
    />
    <v-row>
      <v-col cols="6">
        <v-text-field
          v-model.number="rateRps"
          :label="t('organization.api_keys.rate_rps')"
          type="number"
          class="no-validation"
          clearable
        />
      </v-col>
      <v-col cols="6">
        <v-text-field
          v-model.number="rateBurst"
          :label="t('organization.api_keys.rate_burst')"
          type="number"
          class="no-validation"
          clearable
        />
      </v-col>
    </v-row>

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organization.cancel") }}
      </v-btn>
      <v-btn
        :loading="loading"
        :color="actionOf('save').color"
        :prepend-icon="actionOf('save').icon"
        @click="onSubmit"
      >
        {{ t("organization.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
