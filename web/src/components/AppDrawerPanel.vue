<script setup lang="ts">
/**
 * Detail drawer / filter sheet shell — same header (coloured bar + X),
 * scrolling body and optional footer as AppDialog.
 *
 * `as="drawer"` wraps v-navigation-drawer; `as="sheet"` is the card only
 * (parent supplies v-bottom-sheet).
 */
import { computed, useId } from "vue";
import { useI18n } from "vue-i18n";
import { mdiClose } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import defaults from "@/core/config/VuetifyDefaults";

const props = withDefaults(
  defineProps<{
    modelValue: boolean;
    title?: string;
    width?: number | string;
    location?: "start" | "end" | "left" | "right" | "top" | "bottom";
    as?: "drawer" | "sheet";
    loading?: boolean;
    contentClass?: string;
  }>(),
  {
    title: "",
    width: 440,
    location: "end",
    as: "drawer",
    loading: false,
    contentClass: "pa-4",
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const titleId = useId();
const closeDefaults = defaults.AppDialogClose;

const open = computed({
  get: (): boolean => props.modelValue,
  set: (value: boolean): void => {
    emit("update:modelValue", value);
  },
});

const close = (): void => {
  if (props.loading) return;
  open.value = false;
};
</script>

<template>
  <v-navigation-drawer
    v-if="as === 'drawer'"
    v-model="open"
    :location="location"
    :width="width"
    :aria-labelledby="titleId"
    temporary
    scrim
  >
    <AppCard
      :content-class="contentClass"
      class="app-drawer-panel"
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
              :disabled="loading"
              data-testid="app-drawer-close"
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
  </v-navigation-drawer>

  <AppCard
    v-else
    :content-class="contentClass"
    class="app-panel app-sheet-panel"
    slim
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
            :disabled="loading"
            data-testid="app-drawer-close"
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
</template>
