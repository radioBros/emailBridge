<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import DateUtil from "@/helpers/Date";
import type { AuditRow } from "@/features/organization/composables/useAuditLog";

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  record: AuditRow | null;
}>();

const { t, locale } = useI18n();

const changesJson = computed(() => {
  const changes = props.record?.changes;
  if (!changes || !Object.keys(changes).length) return null;
  const pretty: Record<string, { before: unknown; after: unknown }> = {};
  for (const [field, diff] of Object.entries(changes)) {
    pretty[field] = { before: diff.before ?? null, after: diff.after ?? null };
  }
  return JSON.stringify(pretty, null, 2);
});

const when = computed(() => {
  if (!props.record) return "—";
  return (
    DateUtil.parse(props.record.at)?.setLocale(locale.value).toFormat("dd LLL yyyy HH:mm:ss") ??
    props.record.at
  );
});
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('organization.audit.detail_title')"
    :width="420"
  >
    <template v-if="record">
      <div class="text-body-small text-medium-emphasis mb-4">
        {{ when }}
      </div>

      <div class="mb-3">
        <div class="text-label-large text-medium-emphasis">
          {{ t("organization.audit.col_actor") }}
        </div>
        <div class="text-body-medium">
          {{ record.actor }}
        </div>
      </div>
      <div class="mb-3">
        <div class="text-label-large text-medium-emphasis">
          {{ t("organization.audit.col_action") }}
        </div>
        <div class="text-body-medium">
          {{ record.action }}
        </div>
      </div>
      <div class="mb-3">
        <div class="text-label-large text-medium-emphasis">
          {{ t("organization.audit.col_target") }}
        </div>
        <div class="text-body-medium">
          {{ record.target }}
        </div>
      </div>
      <div class="mb-4">
        <div class="text-label-large text-medium-emphasis">
          {{ t("organization.audit.col_ip") }}
        </div>
        <div class="text-body-medium">
          {{ record.ip }}
        </div>
      </div>

      <div class="text-label-large text-medium-emphasis mb-2">
        {{ t("organization.audit.changes") }}
      </div>
      <pre
        v-if="changesJson"
        class="text-body-small pa-3 rounded bg-surface-light overflow-auto"
      >{{ changesJson }}</pre>
      <p
        v-else
        class="text-body-medium text-medium-emphasis"
      >
        {{ t("organization.audit.no_changes") }}
      </p>
    </template>
  </AppDrawerPanel>
</template>
