<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import {
  METADATA_LIMITS,
  METADATA_TYPES,
  emptyMetadataRow,
  isBlankRow,
  validateMetadataRows,
  type MetadataRow,
  type MetadataType,
} from "@/features/accounts/helpers/accountMetadata";

/** Key / type / value rows for an account's free-form metadata (G4). v-model is the row list. */
const props = withDefaults(
  defineProps<{
    modelValue: MetadataRow[];
    /** Server field errors by metadata key (`""` = the whole object), from a failed save. */
    serverErrors?: Record<string, string>;
    disabled?: boolean;
    /**
     * Offer the "empty (null)" type for new values. Only on create: in a PATCH a null means "remove the key"
     * (JSON Merge Patch), so on edit only a row that is already null keeps the type.
     */
    allowNull?: boolean;
  }>(),
  { serverErrors: () => ({}), disabled: false, allowNull: false },
);

const emit = defineEmits<{ "update:modelValue": [rows: MetadataRow[]] }>();

const { t } = useI18n();
const addAction = actionOf("add");
const removeAction = actionOf("remove");

const validation = computed(() => validateMetadataRows(props.modelValue));
const filledCount = computed(() => props.modelValue.filter((r) => !isBlankRow(r)).length);
const canAdd = computed(() => !props.disabled && props.modelValue.length < METADATA_LIMITS.maxKeys);

const typeItems = computed(() =>
  METADATA_TYPES.map((v) => ({ title: t(`accounts.metadata_type_${v}`), value: v })),
);
const typeItemsFor = (row: MetadataRow) =>
  props.allowNull || row.type === "null" ? typeItems.value : typeItems.value.filter((i) => i.value !== "null");
const boolItems = computed(() => [
  { title: t("accounts.metadata_true"), value: "true" },
  { title: t("accounts.metadata_false"), value: "false" },
]);

const update = (id: number, patch: Partial<MetadataRow>): void => {
  emit(
    "update:modelValue",
    props.modelValue.map((r) => (r.id === id ? { ...r, ...patch } : r)),
  );
};

const onType = (row: MetadataRow, type: MetadataType): void => {
  let value = row.value;
  if (type === "null") value = "";
  else if (type === "boolean") value = row.type === "boolean" ? row.value : "true";
  else if (row.type === "boolean" || row.type === "null") value = "";
  update(row.id, { type, value });
};

const addRow = (): void => {
  if (!canAdd.value) return;
  emit("update:modelValue", [...props.modelValue, emptyMetadataRow()]);
};

const removeRow = (id: number): void => {
  emit(
    "update:modelValue",
    props.modelValue.filter((r) => r.id !== id),
  );
};

const keyError = (row: MetadataRow): string[] => {
  const code = validation.value.rows[row.id]?.key;
  if (code) return [t(`accounts.metadata_err_${code}`)];
  const server = props.serverErrors[row.key.trim()];
  return server ? [server] : [];
};

const valueError = (row: MetadataRow): string[] => {
  const code = validation.value.rows[row.id]?.value;
  return code ? [t(`accounts.metadata_err_${code}`)] : [];
};

const totalError = computed((): string | null => {
  const code = validation.value.total;
  if (code) return t(`accounts.metadata_err_${code}`, { max: METADATA_LIMITS.maxKeys });
  return props.serverErrors[""] ?? null;
});
</script>

<template>
  <div data-testid="metadata-editor">
    <div class="d-flex flex-wrap align-center justify-space-between ga-2 mb-2">
      <div class="min-w-0">
        <div class="text-title-small font-weight-bold">
          {{ t("accounts.metadata_title") }}
        </div>
        <div class="text-body-small text-medium-emphasis">
          {{ t("accounts.metadata_hint", { count: filledCount, max: METADATA_LIMITS.maxKeys }) }}
        </div>
      </div>
      <v-btn-inline
        :color="addAction.color"
        :prepend-icon="addAction.icon"
        :disabled="!canAdd"
        data-testid="metadata-add"
        @click="addRow"
      >
        {{ t("accounts.metadata_add") }}
      </v-btn-inline>
    </div>

    <v-alert
      v-if="totalError"
      type="error"
      class="mb-3"
      data-testid="metadata-total-error"
    >
      {{ totalError }}
    </v-alert>

    <div
      v-if="!modelValue.length"
      class="text-body-medium text-medium-emphasis py-2"
    >
      {{ t("accounts.metadata_empty") }}
    </div>

    <v-row
      v-for="row in modelValue"
      :key="row.id"
      class="align-start"
      data-testid="metadata-row"
    >
      <v-col cols="12" sm="4">
        <v-text-field
          :model-value="row.key"
          :label="t('accounts.metadata_key')"
          :error-messages="keyError(row)"
          :disabled="disabled"
          class="no-validation"
          autocomplete="off"
          spellcheck="false"
          @update:model-value="(v: string) => update(row.id, { key: v })"
        />
      </v-col>
      <v-col cols="5" sm="3">
        <v-select
          :model-value="row.type"
          :items="typeItemsFor(row)"
          :label="t('accounts.metadata_type')"
          :disabled="disabled"
          class="no-validation"
          @update:model-value="(v: MetadataType) => onType(row, v)"
        />
      </v-col>
      <v-col cols="7" sm="4">
        <v-select
          v-if="row.type === 'boolean'"
          :model-value="row.value"
          :items="boolItems"
          :label="t('accounts.metadata_value')"
          :disabled="disabled"
          class="no-validation"
          @update:model-value="(v: string) => update(row.id, { value: v })"
        />
        <v-text-field
          v-else
          :model-value="row.type === 'null' ? '' : row.value"
          :label="t('accounts.metadata_value')"
          :placeholder="row.type === 'null' ? t('accounts.metadata_null_placeholder') : undefined"
          :error-messages="valueError(row)"
          :inputmode="row.type === 'number' ? 'decimal' : undefined"
          :disabled="disabled || row.type === 'null'"
          class="no-validation"
          autocomplete="off"
          @update:model-value="(v: string) => update(row.id, { value: v })"
        />
      </v-col>
      <v-col
        cols="12"
        sm="1"
        class="d-flex justify-end pt-sm-4"
      >
        <v-btn-inline
          :color="removeAction.color"
          :icon="removeAction.icon"
          :aria-label="t('accounts.metadata_remove')"
          :disabled="disabled"
          data-testid="metadata-remove"
          @click="removeRow(row.id)"
        />
      </v-col>
    </v-row>
  </div>
</template>
