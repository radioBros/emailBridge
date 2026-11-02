<script setup lang="ts">
/**
 * Settings-card modal: coloured header + X, scrolling body, fixed footer.
 * Slots-first — title/prepend/header-actions/default/footer.
 */
import { computed, useId } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiClose } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import defaults from "@/core/config/VuetifyDefaults";

defineOptions({ inheritAttrs: false });

const props = withDefaults(
  defineProps<{
    modelValue: boolean;
    title?: string;
    maxWidth?: number | string;
    persistent?: boolean;
    fullscreenOnMobile?: boolean;
    loading?: boolean;
    contentClass?: string;
  }>(),
  {
    title: "",
    maxWidth: 560,
    persistent: false,
    fullscreenOnMobile: false,
    loading: false,
    contentClass: "pa-4",
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const { mobile } = useDisplay();
const titleId = useId();
const closeDefaults = defaults.AppDialogClose;

const open = computed({
  get: (): boolean => props.modelValue,
  set: (value: boolean): void => {
    emit("update:modelValue", value);
  },
});

const fullscreen = computed(
  (): boolean => props.fullscreenOnMobile && mobile.value,
);

const closeDisabled = computed(
  (): boolean => props.persistent && props.loading,
);

const close = (): void => {
  if (closeDisabled.value) return;
  open.value = false;
};
</script>

<template>
  <v-dialog
    v-model="open"
    :max-width="fullscreen ? undefined : maxWidth"
    :fullscreen="fullscreen"
    :persistent="persistent || loading"
    :aria-labelledby="titleId"
    v-bind="$attrs"
  >
    <AppCard
      :content-class="contentClass"
      class="app-panel"
      elevated
    >
      <template #header>
        <div class="d-flex align-center ga-2 w-100 min-w-0">
          <div
            v-if="$slots.prepend"
            class="flex-shrink-0 d-flex align-center"
          >
            <slot name="prepend" />
          </div>
          <div
            :id="titleId"
            class="text-title-medium font-weight-bold text-wrap flex-grow-1 min-w-0"
          >
            <slot name="title">
              {{ title }}
            </slot>
          </div>
          <div
            v-if="$slots['header-actions']"
            class="d-flex align-center ga-1 flex-shrink-0"
          >
            <slot name="header-actions" />
          </div>
          <v-defaults-provider :defaults="closeDefaults">
            <v-btn
              :icon="mdiClose"
              :aria-label="t('common.close')"
              :disabled="closeDisabled"
              data-testid="app-dialog-close"
              @click="close"
            />
          </v-defaults-provider>
        </div>
      </template>

      <slot />

      <template
        v-if="$slots.footer"
        #footer
      >
        <div class="d-flex align-center justify-end flex-wrap ga-2 w-100">
          <slot name="footer" />
        </div>
      </template>
    </AppCard>
  </v-dialog>
</template>
