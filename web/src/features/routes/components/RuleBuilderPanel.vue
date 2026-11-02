<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiPlus, mdiDelete } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { RuleBuilder, RuleBuilderCondition, RuleBuilderOp } from "@/api/routes";
import { actionOf } from "@/constants/Actions";

const props = defineProps<{
  modelValue: RuleBuilder;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: RuleBuilder];
}>();

const { t } = useI18n();
const createAction = actionOf("create");
const deleteAction = actionOf("delete");

const fieldItems = computed(() =>
  [
    "type",
    "account.id",
    "account.externalId",
    "mailbox.path",
    "mailbox.specialUse",
    "from.address",
    "from.domain",
    "to.domain",
    "subject",
    "header",
    "flags",
    "attachments",
    "auth.spf",
    "auth.dkim",
    "auth.dmarc",
  ].map((v) => ({ title: t(`routes.builder_field_${v.replace(/\./g, "_")}`), value: v })),
);

const opItems = computed(() =>
  (
    [
      "eq",
      "neq",
      "in",
      "contains",
      "startsWith",
      "endsWith",
      "matches",
      "exists",
      "notExists",
    ] as RuleBuilderOp[]
  ).map((v) => ({ title: t(`routes.builder_op_${v}`), value: v })),
);

const matchItems = computed(() => [
  { title: t("routes.builder_match_all"), value: "all" },
  { title: t("routes.builder_match_any"), value: "any" },
]);

const needsValue = (op: string): boolean => op !== "exists" && op !== "notExists";

const update = (patch: Partial<RuleBuilder>): void => {
  emit("update:modelValue", { ...props.modelValue, ...patch });
};

const updateCond = (i: number, patch: Partial<RuleBuilderCondition>): void => {
  const conditions = props.modelValue.conditions.map((c, idx) =>
    idx === i ? { ...c, ...patch } : c,
  );
  update({ conditions });
};

const addCond = (): void => {
  update({
    conditions: [
      ...props.modelValue.conditions,
      { field: "type", op: "eq", value: "message.received" },
    ],
  });
};

const removeCond = (i: number): void => {
  update({ conditions: props.modelValue.conditions.filter((_, idx) => idx !== i) });
};

const valueText = (c: RuleBuilderCondition): string => {
  if (Array.isArray(c.value)) return c.value.join(", ");
  if (c.value == null) return "";
  return String(c.value);
};

const setValueText = (i: number, raw: string, op: string): void => {
  if (op === "in") {
    updateCond(i, {
      value: raw
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean),
    });
    return;
  }
  updateCond(i, { value: raw });
};
</script>

<template>
  <div>
    <v-alert
      v-if="disabled"
      type="info"
      class="mb-3"
    >
      {{ t("routes.builder_advanced_notice") }}
    </v-alert>

    <div
      v-if="!disabled"
      class="d-flex flex-wrap align-center ga-3 mb-3"
    >
      <v-btn-toggle
        :model-value="modelValue.match"
        color="accent"
        mandatory
        divided
        @update:model-value="(v) => update({ match: (v as 'all' | 'any') || 'all' })"
      >
        <v-btn
          v-for="m in matchItems"
          :key="m.value"
          :value="m.value"
        >
          {{ m.title }}
        </v-btn>
      </v-btn-toggle>
      <v-spacer />
      <v-btn-inline
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        @click="addCond"
      >
        {{ t("routes.builder_add") }}
      </v-btn-inline>
    </div>

    <div
      v-if="!disabled && modelValue.conditions.length === 0"
      class="text-body-2 text-medium-emphasis mb-2"
    >
      {{ t("routes.builder_empty") }}
    </div>

    <v-sheet
      v-for="(c, i) in modelValue.conditions"
      v-show="!disabled"
      :key="i"
      rounded="lg"
      class="pa-3 mb-3"
      border
    >
      <v-row>
        <v-col
          cols="12"
          md="4"
        >
          <AutocompleteSearch
            :model-value="c.field"
            :items="fieldItems"
            :label="t('routes.builder_field')"
            class="no-validation"
            @update:model-value="(v) => updateCond(i, { field: String(v || 'type') })"
          />
        </v-col>
        <v-col
          v-if="c.field === 'header'"
          cols="12"
          md="3"
        >
          <v-text-field
            :model-value="c.name"
            :label="t('routes.builder_header_name')"
            class="no-validation"
            hide-details
            @update:model-value="(v) => updateCond(i, { name: String(v || '') })"
          />
        </v-col>
        <v-col
          cols="12"
          md="3"
        >
          <AutocompleteSearch
            :model-value="c.op"
            :items="opItems"
            :label="t('routes.builder_op')"
            class="no-validation"
            @update:model-value="(v) => updateCond(i, { op: (v as RuleBuilderOp) || 'eq' })"
          />
        </v-col>
        <v-col
          v-if="needsValue(c.op)"
          cols="12"
          md="4"
        >
          <v-text-field
            :model-value="valueText(c)"
            :label="c.op === 'in' ? t('routes.builder_value_list') : t('routes.builder_value')"
            class="no-validation"
            hide-details
            @update:model-value="(v) => setValueText(i, String(v || ''), c.op)"
          />
        </v-col>
        <v-col
          cols="12"
          class="d-flex justify-end"
        >
          <v-btn-inline
            :color="deleteAction.color"
            :icon="mdiDelete"
            :aria-label="t('routes.builder_remove')"
            @click="removeCond(i)"
          />
        </v-col>
      </v-row>
    </v-sheet>
  </div>
</template>
