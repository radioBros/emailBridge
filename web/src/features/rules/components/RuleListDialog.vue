<script setup lang="ts">
/** Create / edit a named list: name, kind, entries (paste one per line, or import a CSV's first column). */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiFileDelimitedOutline } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import type { RuleList, RuleListKind } from "@/api/rules";
import { actionOf } from "@/constants/Actions";
import { parseEntries, LIST_LIMITS } from "@/features/rules/model/lists";

const props = defineProps<{
  modelValue: boolean;
  list: RuleList | null;
  saving: boolean;
  errorMsg: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [body: { name: string; kind: RuleListKind; entries: string[] }];
}>();

const { t } = useI18n();
const saveAction = actionOf("save");
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const fileInput = ref<HTMLInputElement | null>(null);
const name = ref("");
const kind = ref<RuleListKind>("domains");
const text = ref("");

watch(
  () => [props.modelValue, props.list] as const,
  ([open, l]) => {
    if (!open) return;
    name.value = l?.name ?? "";
    kind.value = l?.kind ?? "domains";
    text.value = (l?.entries ?? []).join("\n");
  },
  { immediate: true },
);

const parsed = computed(() => parseEntries(text.value, kind.value));

const kindItems = computed(() =>
  (["domains", "addresses", "keywords"] as RuleListKind[]).map((k) => ({ title: t(`rules.lists.kind_${k}`), value: k })),
);

const nameRules = [
  (v: unknown): true | string => /^[a-z0-9][a-z0-9_-]{0,63}$/.test(String(v ?? "")) || t("rules.lists.name_invalid"),
];

const entriesRules = [
  (): true | string => parsed.value.entries.length <= LIST_LIMITS.entries || t("rules.lists.too_many", { n: LIST_LIMITS.entries }),
  (): true | string => !parsed.value.tooLong.length || t("rules.lists.too_long", { n: LIST_LIMITS.entryBytes }),
];

const importCsv = async (e: Event): Promise<void> => {
  const file = (e.target as HTMLInputElement).files?.[0];
  if (!file) return;
  const content = await file.text();
  const rows = content
    .split(/\r?\n/)
    .map((r) => (r.split(/[,;\t]/)[0] ?? "").replace(/^"|"$/g, "").trim())
    .filter(Boolean);
  text.value = [text.value.trim(), ...rows].filter(Boolean).join("\n");
  if (fileInput.value) fileInput.value.value = "";
};

const submit = async (): Promise<void> => {
  const res = await formRef.value?.validate();
  if (res && !res.valid) return;
  emit("submit", { name: name.value.trim(), kind: kind.value, entries: parsed.value.entries });
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="list ? t('rules.lists.edit_title', { name: list.name }) : t('rules.lists.create_title')"
    :max-width="620"
    :loading="saving"
    fullscreen-on-mobile
    @update:model-value="(v) => emit('update:modelValue', v)"
  >
    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-4"
    >
      {{ errorMsg }}
    </v-alert>
    <v-form
      ref="formRef"
      @submit.prevent="submit"
    >
      <v-row>
        <v-col
          cols="12"
          md="7"
        >
          <v-text-field
            v-model="name"
            :label="t('rules.lists.name')"
            :rules="nameRules"
            :hint="t('rules.lists.name_hint')"
            data-testid="list-name"
            autocomplete="off"
            persistent-hint
          />
        </v-col>
        <v-col
          cols="12"
          md="5"
        >
          <v-select
            v-model="kind"
            :items="kindItems"
            :label="t('rules.lists.kind')"
            :disabled="Boolean(list)"
            class="no-validation"
          />
        </v-col>
        <v-col cols="12">
          <v-textarea
            v-model="text"
            :label="t('rules.lists.entries')"
            :hint="t('rules.lists.entries_hint', { n: parsed.entries.length, dup: parsed.duplicates })"
            :rules="entriesRules"
            data-testid="list-entries"
            rows="8"
            spellcheck="false"
            persistent-hint
          />
        </v-col>
        <v-col cols="12">
          <input
            ref="fileInput"
            :aria-label="t('rules.lists.import_csv')"
            class="d-none"
            type="file"
            accept=".csv,.txt,text/csv,text/plain"
            @change="importCsv"
          >
          <v-btn-inline
            :prepend-icon="mdiFileDelimitedOutline"
            color="secondary"
            @click="fileInput?.click()"
          >
            {{ t("rules.lists.import_csv") }}
          </v-btn-inline>
        </v-col>
      </v-row>
    </v-form>
    <template #footer>
      <v-btn @click="emit('update:modelValue', false)">
        {{ t("rules.cancel") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="saving"
        data-testid="list-save"
        @click="submit"
      >
        {{ t("rules.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
