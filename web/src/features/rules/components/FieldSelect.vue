<script setup lang="ts">
/**
 * Picks what a condition looks at: the mail-aware fields first (grouped), then every payload field of
 * the rule's event types from the event schemas, shown as a tree with each field's description.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { dataFieldsFor, ACCOUNT_FIELDS, ENVELOPE_FIELDS } from "@/features/rules/catalog/catalog";
import { FIELD_DEFS, type FieldGroup } from "@/features/rules/model/builder";
import { GROUP_ICONS, fieldLabel } from "@/features/rules/components/labels";

const props = defineProps<{
  modelValue: string;
  eventTypes: string[];
  label?: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const { t, te } = useI18n();

interface Item {
  title: string;
  value: string;
  subtitle: string;
  group: FieldGroup;
  depth: number;
  [key: string]: unknown;
}

/** isInbox is reached through the folder row's "Inbox (any provider)" choice. */
const HIDDEN = new Set(["isInbox"]);

const items = computed((): Item[] => {
  const fixed: Item[] = FIELD_DEFS.filter((d) => !HIDDEN.has(d.id)).map((d) => ({
    title: fieldLabel(t, te, d.id),
    value: d.id,
    subtitle: t(`rules.group.${d.group}`),
    group: d.group,
    depth: 0,
  }));
  const fixedIds = new Set(FIELD_DEFS.map((d) => d.id));
  const schema = [...ENVELOPE_FIELDS, ...ACCOUNT_FIELDS, ...dataFieldsFor(props.eventTypes)]
    .filter((f) => !fixedIds.has(f.path) && f.path !== "event.data")
    .map((f) => ({
      title: f.path,
      value: f.path,
      subtitle: f.description ?? t(`rules.type.${f.type}`),
      group: "payload" as const,
      depth: f.path.split(".").length - 2,
    }));
  return [...fixed, ...schema];
});

const value = computed({
  get: (): string => (props.modelValue === "isInbox" ? "folder" : props.modelValue),
  set: (v: string): void => emit("update:modelValue", v),
});
</script>

<template>
  <AutocompleteSearch
    v-model="value"
    :items="items"
    :label="label ?? t('rules.builder.field')"
    :search-fields="['subtitle', 'value']"
    :menu-props="{ maxHeight: 420 }"
    class="no-validation"
    data-testid="rule-field-select"
    hide-details
  >
    <template #item="{ props: itemProps, item }">
      <v-list-item
        :title="(item as Item).title"
        :subtitle="(item as Item).subtitle"
        :prepend-icon="GROUP_ICONS[(item as Item).group]"
        :class="{ 'font-mono': (item as Item).group === 'payload' }"
        :style="{ paddingInlineStart: `${16 + (item as Item).depth * 14}px` }"
        :data-testid="`field-${(item as Item).value}`"
        v-bind="itemProps"
      />
    </template>
  </AutocompleteSearch>
</template>

<style scoped lang="scss">
.font-mono :deep(.v-list-item-title) {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
}
</style>
