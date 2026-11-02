<script setup lang="ts">
/** "Filter & transform" section of the endpoint form: what the rule does today, and the way to the editor. */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiArrowRight, mdiFilterVariant } from "@mdi/js";
import type { EndpointRule } from "@/api/rules";

const props = defineProps<{
  /** Null while the endpoint is being created. */
  endpointId: string | null;
  rule: EndpointRule | null | undefined;
}>();

const { t } = useI18n();

const filter = computed(() => (props.rule?.filter && props.rule.filter !== "true" ? props.rule.filter : ""));
const map = computed(() => props.rule?.map ?? "");
</script>

<template>
  <div
    class="rule-summary d-flex flex-column ga-2 pa-4"
    data-testid="rule-summary"
  >
    <div class="d-flex align-center ga-2">
      <v-avatar-small color="accent">
        <v-icon :icon="mdiFilterVariant" />
      </v-avatar-small>
      <div class="text-title-small font-weight-bold flex-grow-1">
        {{ t("rules.page_title") }}
      </div>
      <v-btn-inline
        v-if="endpointId"
        :to="`/webhooks/${endpointId}/filter`"
        :append-icon="mdiArrowRight"
        color="accent"
        data-testid="rule-summary-open"
      >
        {{ filter || map ? t("rules.summary.edit") : t("rules.summary.add") }}
      </v-btn-inline>
    </div>
    <p
      v-if="!endpointId"
      class="text-body-2 text-medium-emphasis mb-0"
    >
      {{ t("rules.summary.after_create") }}
    </p>
    <template v-else>
      <div class="text-body-2">
        <span class="text-medium-emphasis">{{ t("rules.summary.filter") }}</span>
        <code
          v-if="filter"
          class="summary-code ms-1"
        >{{ filter }}</code>
        <span v-else> {{ t("rules.summary.all_events") }}</span>
      </div>
      <div class="text-body-2">
        <span class="text-medium-emphasis">{{ t("rules.summary.map") }}</span>
        <span v-if="map"> {{ t("rules.summary.custom_body") }}</span>
        <span v-else> {{ t("rules.summary.standard_body") }}</span>
      </div>
    </template>
  </div>
</template>

<style scoped lang="scss">
.rule-summary {
  border: 1px solid rgb(var(--v-theme-accent), 0.25);
  border-radius: 12px;
  background: rgb(var(--v-theme-accent), 0.04);
}

.summary-code {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
  overflow-wrap: anywhere;
}
</style>
