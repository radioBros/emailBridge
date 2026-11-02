<template>
  <div
    v-if="status"
    :aria-label="message || t('common.states.refreshing')"
    :class="{ 'loader-overlay--top': top }"
    aria-live="polite"
    class="loader-overlay"
    role="status"
  >
    <AppCard v-if="hasCaption">
      <div class="d-flex align-center ga-3 pa-2 text-no-wrap">
        <slot>
          <span class="text-body-2">{{ message }}</span>
        </slot>

        <v-progress-circular
          :size="size"
          :aria-label="message || t('common.states.refreshing')"
          color="primary"
          width="2"
          indeterminate
        />
      </div>
    </AppCard>

    <v-progress-circular
      v-else
      :size="size"
      :aria-label="message || t('common.states.refreshing')"
      color="primary"
      indeterminate
    />
  </div>
</template>

<script setup lang="ts">
  import { computed, useSlots } from "vue";
  import { useI18n } from "vue-i18n";

  // Static import, per the static-import carve-out (README §10). An async component resolves a module over the network
  // before it renders anything, which for a loader means the overlay appears *after* the wait it
  // exists to report — and on a fast refetch, never. The thing this component must never do is be
  // slow to say something is slow.
  import AppCard from "@/components/cards/AppCard.vue";

  /**
   * The **refetch** half of the loading rule (README §10) — a scrim and a spinner laid over
   * content that is already on screen.
   *
   * ---
   *
   * **When this, and when a skeleton.**
   *
   * | | Affordance | Because |
   * |---|---|---|
   * | Nothing rendered yet | `LoadingSkeleton` | there is nothing to preserve, and a shape-matched skeleton stops the layout jumping when data lands |
   * | Content on screen, being replaced | **this** | the previous answer is still readable, and swapping it for a skeleton throws away what the user was looking at |
   *
   * The two are mutually exclusive, and `useLoadState` is what guarantees it: `isFirstLoad` and
   * `isRefreshing` are computed from one flag, so a call site cannot accidentally show both. Pass
   * `isRefreshing` here.
   *
   * ---
   *
   * **It needs a positioned ancestor.** The overlay is `position: absolute; inset: 0`, so it fills
   * the nearest positioned box. `AppCard` is already `position: relative`, so inside a card this is
   * automatic. Over anything else — a bare `v-row`, a plain wrapper — add Vuetify's
   * `position-relative` to that container, or the scrim will stretch to the page.
   *
   * **It does not paint immediately.** The CSS carries a 140ms delay, so a refetch that finishes
   * quickly never flashes a spinner across the screen. That delay is the reason this reads as the
   * page working rather than the page breaking.
   */
  const props = withDefaults(
    defineProps<{
      /** Whether a refetch is in flight. Pass `isRefreshing`, never a raw `isLoading`. */
      status?: boolean;
      /** Optional caption beside the spinner — "Ricalcolo in corso…". Renders inside a card. */
      message?: string;
      /** Spinner diameter. 24 reads at section scale; a full-page wait wants more. */
      size?: number;
      /** Pin the spinner near the top, for a section taller than the viewport. */
      top?: boolean;
    }>(),
    { status: false, message: "", size: 24, top: false },
  );

  const { t } = useI18n();
  const slots = useSlots();

  /**
   * Whether to render the captioned card form rather than a bare spinner.
   *
   * Either a `message` or slot content earns the card. A card holding nothing but a spinner is a
   * heavier, boxed version of the spinner alone.
   */
  const hasCaption = computed(() => props.message !== "" || slots["default"] !== undefined);
</script>
