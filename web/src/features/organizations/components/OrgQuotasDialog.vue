<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import type { OrgQuotaKey } from "@/api/organizations";
import type { OrganizationRow } from "@/features/organizations/composables/useOrganizations";

const QUOTA_KEYS: OrgQuotaKey[] = [
  "accounts",
  "sendsPerDay",
  "sendsPerAccountMinute",
  "endpoints",
];

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  organization: OrganizationRow | null;
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [quotas: Partial<Record<OrgQuotaKey, number | null>>];
}>();

const { t } = useI18n();
const saveAction = actionOf("save");
const form = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);

type QuotaField = { unlimited: boolean; value: string };

const fields = ref<Record<OrgQuotaKey, QuotaField>>({
  accounts: { unlimited: true, value: "" },
  sendsPerDay: { unlimited: true, value: "" },
  sendsPerAccountMinute: { unlimited: true, value: "" },
  endpoints: { unlimited: true, value: "" },
});

const readQuota = (raw: unknown, key: OrgQuotaKey): QuotaField => {
  const q = raw as Record<string, unknown> | undefined;
  const n = q?.[key];
  if (n == null || n === 0 || n === "") {
    return { unlimited: true, value: "" };
  }
  const num = Number(n);
  if (!Number.isFinite(num) || num <= 0) {
    return { unlimited: true, value: "" };
  }
  return { unlimited: false, value: String(Math.trunc(num)) };
};

watch(open, (v) => {
  if (!v || !props.organization) return;
  const q = props.organization.raw.quotas;
  for (const key of QUOTA_KEYS) {
    fields.value[key] = readQuota(q, key);
  }
});

const numberRules = [
  (v: string): true | string => {
    const n = parseInt(String(v), 10);
    if (!Number.isFinite(n) || String(n) !== String(v).trim() || n < 1) {
      return t("organizations.quota_invalid");
    }
    return true;
  },
];

const quotaPayload = (): Partial<Record<OrgQuotaKey, number | null>> => {
  const out: Partial<Record<OrgQuotaKey, number | null>> = {};
  for (const key of QUOTA_KEYS) {
    const f = fields.value[key];
    if (f.unlimited) {
      out[key] = null;
    } else {
      const n = parseInt(f.value, 10);
      out[key] = Number.isFinite(n) && n > 0 ? n : null;
    }
  }
  return out;
};

const subtitle = computed(() => props.organization?.name ?? "");

const onSubmit = async (): Promise<void> => {
  const res = await form.value?.validate();
  if (!res?.valid) return;
  emit("submit", quotaPayload());
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organizations.quotas_title')"
    :max-width="560"
    :loading="loading"
  >
    <p
      v-if="subtitle"
      class="text-body-medium text-medium-emphasis mb-4"
    >
      {{ subtitle }}
    </p>

    <v-form
      ref="form"
      @submit.prevent="onSubmit"
    >
      <div class="d-flex flex-column ga-4">
        <div
          v-for="key in QUOTA_KEYS"
          :key="key"
        >
          <v-switch
            v-model="fields[key].unlimited"
            :label="t('organizations.quota_unlimited', { key: t(`organizations.quota_${key}`) })"
            color="primary"
            class="mb-1"
            hide-details
          />
          <p class="text-body-small text-medium-emphasis mb-2">
            {{ t(`organizations.quota_help_${key}`) }}
          </p>
          <v-text-field
            v-if="!fields[key].unlimited"
            v-model="fields[key].value"
            :label="t(`organizations.quota_${key}`)"
            :rules="numberRules"
            type="number"
            min="1"
            step="1"
          />
        </div>
      </div>
    </v-form>

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organizations.cancel") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="loading"
        @click="onSubmit"
      >
        {{ t("organizations.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
