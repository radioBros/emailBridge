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
  type ApiKeyCreateBody,
} from "@/api/apikeys";
import {
  baseScopesOf,
  granularScope,
  MCP_SCOPE,
  scopeItems,
  type GranularResource,
} from "@/features/organization/helpers/scopes";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";

const open = defineModel<boolean>("open", { default: false });

defineProps<{
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [body: ApiKeyCreateBody];
}>();

const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const form = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const name = ref("");
const scopes = ref<string[]>([]);
const accountId = ref<string | null>(null);
const expiresAt = ref("");
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

watch(open, (v) => {
  if (!v) return;
  name.value = "";
  scopes.value = [];
  accountId.value = null;
  expiresAt.value = "";
  ipsText.value = "";
  referrersText.value = "";
  rateRps.value = null;
  rateBurst.value = null;
  granularResource.value = null;
  granularMode.value = "read";
  granularId.value = null;
  void loadAccounts("");
});

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
    endpointItems.value = (res.data ?? []).map((e) => ({
      title: e.name,
      value: e.id,
    }));
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
    routeItems.value = (res.data ?? []).map((r) => ({
      title: r.name,
      value: r.id,
    }));
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

const canSubmit = computed(() => name.value.trim() && scopes.value.length > 0);

const lines = (text: string): string[] =>
  text
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter(Boolean);

const onSubmit = async (): Promise<void> => {
  const res = await form.value?.validate();
  if (!res?.valid || !canSubmit.value) return;
  const body: ApiKeyCreateBody = {
    name: name.value.trim(),
    scopes: [...baseScopesOf(scopes.value), ...scopes.value.filter((s) => s.split(":").length === 3)],
  };
  // de-dupe while preserving order
  body.scopes = [...new Set(body.scopes)];
  if (accountId.value) body.accountId = accountId.value;
  if (expiresAt.value) body.expiresAt = new Date(expiresAt.value).toISOString();
  const ips = lines(ipsText.value);
  const referrers = lines(referrersText.value);
  if (ips.length || referrers.length) {
    body.restrictions = {};
    if (ips.length) body.restrictions.ips = ips;
    if (referrers.length) body.restrictions.referrers = referrers;
  }
  if (rateRps.value != null || rateBurst.value != null) {
    body.rateLimit = {};
    if (rateRps.value != null) body.rateLimit.rps = rateRps.value;
    if (rateBurst.value != null) body.rateLimit.burst = rateBurst.value;
  }
  emit("submit", body);
};

const onResourceSearch = (q: string): void => {
  if (granularResource.value === "webhooks") void loadEndpoints(q);
  else if (granularResource.value === "routes") void loadRoutes(q);
  else void loadAccounts(q);
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organization.api_keys.create_title')"
    :max-width="640"
    :loading="loading"
    fullscreen-on-mobile
  >
    <v-form
      ref="form"
      @submit.prevent="onSubmit"
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

      <AutocompleteSearch
        v-model="accountId"
        :items="accountItems"
        :loading="resourceLoading && granularResource === 'accounts'"
        :label="t('organization.api_keys.account_optional')"
        class="mb-2 mt-2 no-validation"
        clearable
        server-search
        @update:search="loadAccounts"
      />
      <v-text-field
        v-model="expiresAt"
        :label="t('organization.api_keys.col_expires')"
        type="datetime-local"
        class="mb-2 no-validation"
        clearable
      />
      <v-textarea
        v-model="ipsText"
        :label="t('organization.api_keys.restrictions_ips')"
        :hint="t('organization.api_keys.restrictions_ips_hint')"
        class="mb-2 no-validation"
        rows="2"
        auto-grow
        persistent-hint
      />
      <v-textarea
        v-model="referrersText"
        :label="t('organization.api_keys.restrictions_referrers')"
        :hint="t('organization.api_keys.restrictions_referrers_hint')"
        class="mb-2 no-validation"
        rows="2"
        auto-grow
        persistent-hint
      />
      <v-row>
        <v-col
          cols="6"
        >
          <v-text-field
            v-model.number="rateRps"
            :label="t('organization.api_keys.rate_rps')"
            type="number"
            class="no-validation"
            clearable
          />
        </v-col>
        <v-col
          cols="6"
        >
          <v-text-field
            v-model.number="rateBurst"
            :label="t('organization.api_keys.rate_burst')"
            type="number"
            class="no-validation"
            clearable
          />
        </v-col>
      </v-row>
    </v-form>

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organization.cancel") }}
      </v-btn>
      <v-btn
        :loading="loading"
        :disabled="!canSubmit"
        :color="actionOf('create').color"
        :prepend-icon="actionOf('create').icon"
        @click="onSubmit"
      >
        {{ t("organization.api_keys.create") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
