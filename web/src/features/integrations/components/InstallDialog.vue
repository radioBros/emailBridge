<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { IntegrationCatalogueItem } from "@/api/integrations";
import AppDialog from "@/components/AppDialog.vue";
import {
  defaultsFromSchema,
  schemaProperties,
} from "@/features/integrations/helpers/schema";

const props = defineProps<{
  modelValue: boolean;
  item: IntegrationCatalogueItem | null;
  saving?: boolean;
  error?: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [boolean];
  save: [payload: { name?: string; config: Record<string, unknown>; secrets: Record<string, string>; runTest: boolean }];
}>();

const { t, te } = useI18n();
const name = ref("");
const values = ref<Record<string, unknown>>({});
const secrets = ref<Record<string, string>>({});
const runTest = ref(false);

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const fields = computed(() =>
  schemaProperties(props.item?.configSchema, props.item?.secretFields ?? []),
);

watch(
  () => [props.modelValue, props.item] as const,
  ([isOpen, item]) => {
    if (!isOpen || !item) return;
    name.value = item.name;
    values.value = { ...defaultsFromSchema(item.configSchema) };
    secrets.value = {};
    runTest.value = false;
  },
);

const onSave = (): void => {
  const config: Record<string, unknown> = {};
  const sec: Record<string, string> = {};
  for (const f of fields.value) {
    if (f.writeOnly) {
      const v = secrets.value[f.key];
      if (v) sec[f.key] = v;
      continue;
    }
    const v = values.value[f.key];
    if (v !== undefined && v !== "") config[f.key] = v;
  }
  emit("save", {
    name: name.value.trim() || undefined,
    config,
    secrets: sec,
    runTest: runTest.value,
  });
};
</script>

<template>
  <AppDialog
    v-model="open"
    :max-width="560"
    :loading="saving"
    fullscreen-on-mobile
  >
    <template
      v-if="item"
      #prepend
    >
      <v-avatar
        :color="item.color || 'primary'"
      >
        <span class="text-white text-caption font-weight-bold">
          {{ item.name.slice(0, 2).toUpperCase() }}
        </span>
      </v-avatar>
    </template>
    <template
      v-if="item"
      #title
    >
      <div class="min-width-0">
        <div class="text-title-medium font-weight-bold">
          {{ t("integrations.install_title", { name: item.name }) }}
        </div>
        <div class="text-body-small text-medium-emphasis text-truncate">
          {{
            te(`integrations.desc_${item.id}`)
              ? t(`integrations.desc_${item.id}`)
              : item.description
          }}
        </div>
      </div>
    </template>

    <template v-if="item">
      <v-alert
        v-if="error"
        type="error"
        class="mb-4"
      >
        {{ error }}
      </v-alert>
      <v-text-field
        v-model="name"
        :label="t('integrations.field_name')"
        class="mb-2 no-validation"
        no-validation
      />
      <template
        v-for="f in fields"
        :key="f.key"
      >
        <v-switch
          v-if="f.type === 'boolean'"
          v-model="values[f.key]"
          :label="f.title"
          :hint="f.description"
          color="primary"
          class="mb-2"
          persistent-hint
          no-validation
        />
        <v-text-field
          v-else-if="f.writeOnly"
          v-model="secrets[f.key]"
          :label="f.title"
          :hint="f.description"
          :type="'password'"
          :required="f.required"
          class="mb-2 no-validation"
          autocomplete="new-password"
          persistent-hint
          no-validation
        />
        <v-textarea
          v-else-if="f.key === 'events' || f.key === 'filter'"
          v-model="values[f.key] as string"
          :label="f.title"
          :hint="f.description"
          :required="f.required"
          rows="2"
          class="mb-2 no-validation"
          persistent-hint
          no-validation
        />
        <v-text-field
          v-else
          v-model="values[f.key] as string"
          :label="f.title"
          :hint="f.description"
          :required="f.required"
          :type="f.format === 'uri' ? 'url' : 'text'"
          class="mb-2 no-validation"
          persistent-hint
          no-validation
        />
      </template>
      <v-switch
        v-if="item.capabilities?.includes('test')"
        v-model="runTest"
        :label="t('integrations.run_test')"
        color="primary"
        class="mt-2"
        no-validation
      />
    </template>

    <template #footer>
      <v-btn @click="open = false">
        {{ t("integrations.cancel") }}
      </v-btn>
      <v-btn
        :loading="saving"
        color="success"
        @click="onSave"
      >
        {{ t("integrations.install") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
