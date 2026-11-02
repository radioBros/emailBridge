<script setup lang="ts">
// =========================================================================
// ConfirmDialog — the global modal, mounted ONCE per layout.
// =========================================================================
//
// Driven entirely by `useConfirmStore`. Callers never touch this component;
// they call `useConfirm()` and await a boolean.
//
// ⚠️ Mount exactly ONE of these on the page — in each layout's template, beside
// <router-view />. Two mounted at once both render the same store state, so you
// get two stacked dialogs and the first Esc resolves only one of them.
//
// COLOUR COMES FROM THE VUETIFY THEME, not from this file. `variant` maps to the
// theme's `error` / `warning` / `info`, so the dialog inherits your palette and
// changes with it.

import { computed } from "vue";
import { mdiAlert, mdiAlertCircle, mdiInformation } from "@mdi/js";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import { useConfirmStore, type ConfirmVariant } from "@/composables/useConfirm";

const { t } = useI18n();
const store = useConfirmStore();

/** Variant → the Vuetify theme colour and the default icon. */
const VARIANTS: Record<ConfirmVariant, { color: string; icon: string }> = {
  info: { color: "info", icon: mdiInformation },
  warning: { color: "warning", icon: mdiAlert },
  danger: { color: "error", icon: mdiAlertCircle },
};

const isOpen = computed({
  get: (): boolean => store.open,
  set: (value: boolean): void => {
    // Only the STORE may flip `open` to false, so the promise always settles.
    // Any outside close (Esc, backdrop, X) is therefore treated as a cancel.
    if (!value) store.cancel();
  },
});

const variant = computed<ConfirmVariant>(() => store.current?.variant ?? "info");
const theme = computed(() => VARIANTS[variant.value]);
const dismissible = computed<boolean>(() => store.current?.dismissible !== false);
const icon = computed<string>(() => store.current?.icon ?? theme.value.icon);
const title = computed<string>(() => store.current?.title ?? "");
const body = computed<string>(() => store.current?.body ?? "");
const confirmText = computed<string>(() => store.current?.confirmText ?? t("confirm.confirm"));
const cancelText = computed<string>(() => store.current?.cancelText ?? t("confirm.cancel"));

const onCancel = (): void => store.cancel();
const onConfirm = async (): Promise<void> => {
  try {
    await store.accept();
  } catch {
    // Swallowed at the dialog level BY DESIGN: the caller's `onConfirm` owns
    // surfacing the error (toast, banner). Here we only keep the dialog open so
    // the user can retry — rethrowing would surface as an unhandled rejection.
  }
};
</script>

<template>
  <AppDialog
    v-model="isOpen"
    :title="title"
    :persistent="!dismissible || store.loading"
    :loading="store.loading"
    :max-width="460"
  >
    <template #prepend>
      <v-avatar
        :color="theme.color"
      >
        <v-icon
          :icon="icon"
        />
      </v-avatar>
    </template>

    <p
      v-if="body"
      class="text-body-1 text-medium-emphasis mb-0"
    >
      {{ body }}
    </p>

    <template #footer>
      <v-btn
        :disabled="store.loading"
        @click="onCancel"
      >
        {{ cancelText }}
      </v-btn>
      <v-btn
        :color="theme.color"
        :loading="store.loading"
        @click="onConfirm"
      >
        {{ confirmText }}
      </v-btn>
    </template>
  </AppDialog>
</template>
