<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import { actionOf } from "@/constants/Actions";
import { useDeliveryTest } from "@/features/gateways/composables/useDeliveryTest";
import {
  authResultColor,
  authResultLabelKey,
  folderLabelKey,
  stateLabelKey,
} from "@/features/gateways/helpers/labels";

const { t, te } = useI18n();
const testAction = actionOf("sendTest");

const {
  accounts,
  accountsLoading,
  accountId,
  to,
  running,
  result,
  error,
  loadAccounts,
  start,
} = useDeliveryTest();

onMounted(() => {
  void loadAccounts();
});

const accountItems = computed(() =>
  accounts.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const errorText = computed(() => {
  if (!error.value) return null;
  if (error.value === "required") return t("gateways.delivery_required");
  return error.value;
});

const stateLabel = (s: string): string => {
  const key = stateLabelKey(s);
  return te(key) ? t(key) : s;
};

const folderLabel = (f: string): string => {
  const key = folderLabelKey(f);
  return te(key) ? t(key) : f;
};

const authLabel = (r: string): string => {
  const key = authResultLabelKey(r);
  return te(key) ? t(key) : r;
};

const onAccountSearch = (q: string): void => {
  void loadAccounts(q);
};
</script>

<template>
  <AppCard>
    <div class="text-h6 font-weight-bold mb-1">
      {{ t("gateways.delivery_title") }}
    </div>
    <p class="text-body-medium text-medium-emphasis mb-4">
      {{ t("gateways.delivery_subtitle") }}
    </p>

    <div class="position-relative">
      <AppSectionLoader :status="running && !result" />

      <v-row>
        <v-col
          cols="12"
          md="5"
        >
          <AutocompleteSearch
            v-model="accountId"
            :items="accountItems"
            :label="t('gateways.delivery_account')"
            :loading="accountsLoading"
            class="no-validation"
            data-testid="delivery-account"
            clearable
            hide-details
            server-search
            @update:search="onAccountSearch"
          />
        </v-col>
        <v-col
          cols="12"
          md="5"
        >
          <v-text-field
            v-model="to"
            :label="t('gateways.delivery_to')"
            class="no-validation"
            data-testid="delivery-to"
            type="email"
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          md="2"
          class="d-flex align-center"
        >
          <v-btn
            :color="testAction.color"
            :prepend-icon="testAction.icon"
            :loading="running"
            data-testid="delivery-start"
            block
            @click="start"
          >
            {{ running ? t("gateways.delivery_running") : t("gateways.delivery_start") }}
          </v-btn>
        </v-col>
      </v-row>

      <v-alert
        v-if="errorText"
        type="error"
        class="mt-4"
      >
        {{ errorText }}
      </v-alert>

      <div
        v-if="result"
        class="mt-4"
      >
        <div class="d-flex flex-wrap align-center ga-2 mb-3">
          <v-chip
            color="info"
          >
            {{ t("gateways.delivery_state") }}:
            {{ stateLabel(result.state) }}
          </v-chip>
          <v-chip
            v-if="result.folder"
            color="secondary"
          >
            {{ t("gateways.delivery_folder") }}:
            {{ folderLabel(result.folder) }}
          </v-chip>
          <span
            v-if="result.latencyMs != null"
            class="text-body-small text-medium-emphasis"
          >
            {{ t("gateways.delivery_latency") }}: {{ result.latencyMs }} ms
          </span>
        </div>
        <div
          v-if="result.mailboxPath"
          class="text-body-small text-medium-emphasis mb-3"
        >
          {{ t("gateways.delivery_mailbox") }}: {{ result.mailboxPath }}
        </div>
        <div
          v-if="result.authResults"
          class="d-flex flex-wrap ga-2"
        >
          <v-chip
            :color="authResultColor(result.authResults.spf?.result ?? 'none')"
          >
            {{ t("gateways.auth_spf") }}:
            {{ authLabel(result.authResults.spf?.result ?? "none") }}
          </v-chip>
          <v-chip
            :color="authResultColor(result.authResults.dmarc?.result ?? 'none')"
          >
            {{ t("gateways.auth_dmarc") }}:
            {{ authLabel(result.authResults.dmarc?.result ?? "none") }}
          </v-chip>
          <v-chip
            v-for="(d, i) in result.authResults.dkim ?? []"
            :key="i"
            :color="authResultColor(d.result)"
          >
            {{ t("gateways.auth_dkim") }}:
            {{ authLabel(d.result) }}
          </v-chip>
        </div>
        <v-alert
          v-if="result.error"
          type="error"
          class="mt-3"
        >
          {{ result.error.detail || result.error.code || result.error.title }}
        </v-alert>
      </div>
    </div>
  </AppCard>
</template>
