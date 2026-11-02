<script setup lang="ts">
/** Server-searched account picker (name, e-mail, external ID); shows names, never raw IDs. */
import type { ListOption } from "@/features/rules/components/listItem";
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { AccountView } from "@/api/accounts";
import { useRuleContext } from "@/features/rules/composables/useRuleContext";

const props = withDefaults(
  defineProps<{
    modelValue: string | string[] | null;
    label?: string;
    multiple?: boolean;
  }>(),
  { label: "", multiple: false },
);

const emit = defineEmits<{
  "update:modelValue": [value: string | string[] | null];
}>();

const { t } = useI18n();
const ctx = useRuleContext();
const results = ref<AccountView[]>([]);
const loading = ref(false);

const selectedIds = computed((): string[] =>
  (Array.isArray(props.modelValue) ? props.modelValue : [props.modelValue]).filter(
    (v): v is string => typeof v === "string" && v !== "",
  ),
);

const search = async (q: string): Promise<void> => {
  loading.value = true;
  results.value = await ctx.searchAccounts(q);
  loading.value = false;
};

onMounted(() => {
  void search("");
});

watch(
  selectedIds,
  (ids) => {
    for (const id of ids) void ctx.ensureAccount(id);
  },
  { immediate: true },
);

const items = computed(() => {
  const seen = new Set<string>();
  const out: Array<{ title: string; value: string; subtitle: string }> = [];
  const push = (a: AccountView): void => {
    if (seen.has(a.id)) return;
    seen.add(a.id);
    out.push({
      title: a.name || a.email,
      value: a.id,
      subtitle: [a.email, a.externalId].filter(Boolean).join(" · "),
    });
  };
  for (const id of selectedIds.value) {
    const a = ctx.accounts[id];
    if (a) push(a);
    else if (!seen.has(id)) {
      seen.add(id);
      out.push({ title: id, value: id, subtitle: "" });
    }
  }
  results.value.forEach(push);
  return out;
});

const value = computed({
  get: () => props.modelValue,
  set: (v) => emit("update:modelValue", v as string | string[] | null),
});
</script>

<template>
  <AutocompleteSearch
    v-model="value"
    :items="items"
    :label="label || t('rules.builder.account')"
    :loading="loading"
    :multiple="multiple"
    :chips="multiple"
    :closable-chips="multiple"
    class="no-validation"
    data-testid="rule-account-select"
    server-search
    hide-details
    @update:search="search"
  >
    <template #item="{ props: itemProps, item }">
      <v-list-item
        :title="(item as ListOption).title"
        :subtitle="(item as ListOption).subtitle"
        v-bind="itemProps"
      />
    </template>
  </AutocompleteSearch>
</template>
