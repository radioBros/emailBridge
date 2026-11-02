<script setup lang="ts">
/**
 * Ready-made rules. A preset that sends a shared key asks for the NAME of the `settings.env` secret
 * that holds it (never the value in the rule); the value can optionally be stored right here, write-only.
 */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiArrowLeft, mdiKeyChainVariant } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import AppCard from "@/components/cards/AppCard.vue";
import { actionOf } from "@/constants/Actions";
import { DEFAULT_SECRET_NAME, PRESETS, type Preset } from "@/features/rules/model/presets";
import { SECRET_NAME_RE } from "@/features/rules/model/mapTree";

const props = defineProps<{
  modelValue: boolean;
  /** Presets that only change the filter (routes have no event subscription) are still shown. */
  applying: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  apply: [payload: { preset: Preset; secretName: string; secretValue: string }];
}>();

const { t } = useI18n();
const applyAction = actionOf("apply");
const chosen = ref<Preset | null>(null);
const secretName = ref(DEFAULT_SECRET_NAME);
const secretValue = ref("");
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    chosen.value = null;
    secretName.value = DEFAULT_SECRET_NAME;
    secretValue.value = "";
  },
);

const nameRules = [(v: unknown): true | string => SECRET_NAME_RE.test(String(v ?? "")) || t("rules.map.secret_invalid")];

const choose = (p: Preset): void => {
  if (p.needsSecret) {
    chosen.value = p;
    return;
  }
  emit("apply", { preset: p, secretName: "", secretValue: "" });
};

const apply = async (): Promise<void> => {
  if (!chosen.value) return;
  const res = await formRef.value?.validate();
  if (res && !res.valid) return;
  emit("apply", { preset: chosen.value, secretName: secretName.value.trim(), secretValue: secretValue.value });
};

const title = computed(() => (chosen.value ? t(`rules.preset.${chosen.value.id}.title`) : t("rules.preset.title")));
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="title"
    :max-width="760"
    :loading="applying"
    fullscreen-on-mobile
    @update:model-value="(v) => emit('update:modelValue', v)"
  >
    <template
      v-if="chosen"
      #prepend
    >
      <v-btn-affix
        :icon="mdiArrowLeft"
        :aria-label="t('rules.preset.back')"
        @click="chosen = null"
      />
    </template>

    <v-row v-if="!chosen">
      <v-col
        v-for="p in PRESETS"
        :key="p.id"
        cols="12"
        sm="6"
      >
        <AppCard
          :data-testid="`preset-${p.id}`"
          class="h-100"
          content-class="pa-4 d-flex ga-3 align-start"
          selectable
          @click="choose(p)"
        >
          <v-avatar :color="p.color">
            <v-icon :icon="p.icon" />
          </v-avatar>
          <div class="min-w-0">
            <div class="text-title-small font-weight-bold mb-1">
              {{ t(`rules.preset.${p.id}.title`) }}
            </div>
            <div class="text-body-2 text-medium-emphasis">
              {{ t(`rules.preset.${p.id}.body`) }}
            </div>
          </div>
        </AppCard>
      </v-col>
    </v-row>

    <v-form
      v-else
      ref="formRef"
      class="d-flex flex-column ga-4"
      @submit.prevent="apply"
    >
      <p class="text-body-2 mb-0">
        {{ t(`rules.preset.${chosen.id}.body`) }}
      </p>
      <v-alert
        :icon="mdiKeyChainVariant"
        type="info"
      >
        {{ t("rules.preset.secret_explain") }}
      </v-alert>
      <v-text-field
        v-model="secretName"
        :label="t('rules.preset.secret_name')"
        :rules="nameRules"
        :hint="t('rules.preset.secret_name_hint', { name: secretName || DEFAULT_SECRET_NAME })"
        data-testid="preset-secret-name"
        autocomplete="off"
        persistent-hint
      >
        <template #prepend-inner>
          <span class="text-medium-emphasis font-mono">env.</span>
        </template>
      </v-text-field>
      <v-text-field
        v-model="secretValue"
        :label="t('rules.preset.secret_value')"
        :hint="t('rules.preset.secret_value_hint')"
        class="no-validation"
        type="password"
        autocomplete="new-password"
        data-testid="preset-secret-value"
        persistent-hint
      />
    </v-form>

    <template
      v-if="chosen"
      #footer
    >
      <v-btn @click="chosen = null">
        {{ t("rules.preset.back") }}
      </v-btn>
      <v-btn
        :color="applyAction.color"
        :prepend-icon="applyAction.icon"
        :loading="applying"
        data-testid="preset-apply"
        @click="apply"
      >
        {{ t("rules.preset.apply") }}
      </v-btn>
    </template>
  </AppDialog>
</template>

<style scoped lang="scss">
.font-mono {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}
</style>
