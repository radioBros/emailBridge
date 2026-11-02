// =========================================================================
// useConfirm — one global confirm dialog, callable from anywhere.
// =========================================================================
//
// A Pinia store plus a thin composable over it. `components/ConfirmDialog.vue`
// is mounted ONCE per layout and renders whatever the store currently holds;
// callers never import the component or own a `v-model` boolean.
//
// Use it for every destructive or visible-consequence action, so all of them
// look identical instead of each screen inventing its own modal.
//
//   const confirm = useConfirm();
//   const ok = await confirm({
//     title: "Delete this program?",
//     body: "Customers will no longer be able to collect stamps.",
//     variant: "danger",
//     confirmText: "Delete",
//   });
//   if (!ok) return;
//
// Requires `pinia`.

import { defineStore } from "pinia";
import { ref, type Ref } from "vue";

/**
 * Semantic severity. It drives the icon and the confirm button's colour, which
 * come from the Vuetify theme — so "danger" is your theme's `error`, not a hex
 * value this file invented.
 *
 *   danger  — irreversible data loss (delete, revoke)
 *   warning — visible consequences others will notice (suspend, unpublish)
 *   info    — session or state changes (reload for update, sign out)
 */
export type ConfirmVariant = "danger" | "warning" | "info";

export interface ConfirmOptions {
  /** The headline question. e.g. "Suspend the program?" */
  title: string;
  /** Body sentence explaining WHAT happens. Always provide this. */
  body: string;
  /** Icon name shown in the badge. Defaults per variant. */
  icon?: string;
  /** Visual + semantic severity. Defaults to "info". */
  variant?: ConfirmVariant;
  /** Confirm-button label. Defaults to the `confirm.*` i18n string. */
  confirmText?: string;
  /** Cancel-button label. Defaults to the `confirm.*` i18n string. */
  cancelText?: string;
  /** If false, Esc + backdrop click are disabled. Defaults to true. */
  dismissible?: boolean;
  /**
   * Optional async handler. When provided the dialog AWAITS it on confirm,
   * showing a spinner on the confirm button, and closes only once it settles.
   * If it THROWS, the dialog stays open so the caller can surface the error and
   * let the user retry.
   *
   * When omitted the confirm button resolves immediately and the caller does its
   * async work after `await confirm(...)`.
   */
  onConfirm?: () => Promise<void> | void;
}

interface PendingConfirm extends ConfirmOptions {
  resolve: (result: boolean) => void;
}

export const useConfirmStore = defineStore("confirm", () => {
  const open = ref(false);
  const loading = ref(false);
  const current: Ref<PendingConfirm | null> = ref(null);

  const ask = (options: ConfirmOptions): Promise<boolean> => {
    // If a previous dialog is somehow still pending, resolve it as CANCELLED
    // before showing the new one — last call wins. Without this the earlier
    // promise never settles and its `await` hangs for the life of the tab.
    if (current.value) {
      current.value.resolve(false);
    }

    return new Promise<boolean>((resolve) => {
      current.value = { ...options, resolve };
      loading.value = false;
      open.value = true;
    });
  };

  const accept = async (): Promise<void> => {
    const pending = current.value;
    if (!pending || loading.value) return;

    if (pending.onConfirm) {
      loading.value = true;
      try {
        await pending.onConfirm();
      } catch (err) {
        // Keep the dialog OPEN so the caller can show an error and let the user
        // retry or cancel. Re-thrown so the failure is not swallowed here.
        loading.value = false;
        throw err;
      }
      loading.value = false;
    }

    open.value = false;
    pending.resolve(true);
    current.value = null;
  };

  const cancel = (): void => {
    const pending = current.value;
    if (!pending || loading.value) return;

    open.value = false;
    pending.resolve(false);
    current.value = null;
  };

  return { open, loading, current, ask, accept, cancel };
});

/**
 * Callable wrapper around the store: opens the global dialog and resolves with
 * the user's choice.
 */
export const useConfirm = (): ((options: ConfirmOptions) => Promise<boolean>) => {
  const store = useConfirmStore();
  return (options: ConfirmOptions): Promise<boolean> => store.ask(options);
};
