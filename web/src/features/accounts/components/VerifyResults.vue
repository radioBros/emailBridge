<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCheckCircle, mdiCloseCircle } from "@mdi/js";
import type { VerifyProtocolResult, VerifyResult } from "@/api/accounts";
import AppCard from "@/components/cards/AppCard.vue";
import { useProblem } from "@/composables/useProblem";

const props = defineProps<{
  result: VerifyResult | null;
  error: string | null;
  loading?: boolean;
}>();

const { t } = useI18n();
const { messageOf } = useProblem();

const rows = computed(() => {
  if (!props.result) return [];
  const out: { key: string; label: string; r?: VerifyProtocolResult }[] = [];
  out.push({ key: "imap", label: t("accounts.proto_imap"), r: props.result.imap });
  out.push({ key: "smtp", label: t("accounts.proto_smtp"), r: props.result.smtp });
  return out;
});

const problemTitle = (code?: string): string => {
  if (!code) return "";
  return messageOf({ code });
};
</script>

<template>
  <div>
    <v-alert
      v-if="error"
      type="error"
      class="mb-4"
    >
      {{ error }}
    </v-alert>

    <v-progress-linear
      v-if="loading"
      color="info"
      class="mb-4"
      indeterminate
    />

    <v-row v-if="result">
      <v-col
        v-for="row in rows"
        :key="row.key"
        cols="12"
        md="6"
      >
        <AppCard>
          <div class="d-flex align-center ga-3 mb-2">
            <v-icon-status
              :icon="row.r?.ok ? mdiCheckCircle : mdiCloseCircle"
              :color="row.r?.ok ? 'success' : 'error'"
            />
            <div>
              <div class="text-title-medium font-weight-bold">
                {{ row.label }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{
                  row.r?.ok
                    ? t("accounts.verify_ok", { ms: row.r.latencyMs ?? "—" })
                    : t("accounts.verify_fail")
                }}
              </div>
            </div>
          </div>
          <v-alert
            v-if="row.r && !row.r.ok"
            type="error"
          >
            <div
              v-if="row.r.code"
              class="d-flex flex-wrap align-center ga-2 mb-1"
            >
              <span class="text-body-small font-weight-medium">
                {{ problemTitle(row.r.code) }}
              </span>
              <v-chip
                v-tippy="row.r.code"
                class="font-mono"
                label
              >
                {{ row.r.code }}
              </v-chip>
            </div>
            {{ row.r.detail || t("accounts.verify_no_detail") }}
          </v-alert>
          <div
            v-if="row.r?.capabilities?.length"
            class="text-body-small text-medium-emphasis mt-2"
          >
            {{ row.r.capabilities.join(", ") }}
          </div>
        </AppCard>
      </v-col>
    </v-row>

    <p
      v-else-if="!loading && !error"
      class="text-body-medium text-medium-emphasis mb-0"
    >
      {{ t("accounts.verify_hint") }}
    </p>
  </div>
</template>
