<template>
  <div
    :class="rootClasses"
    class="app-card"
    @click="onClick"
  >
    <div
      v-if="$slots.header"
      class="app-card-header"
    >
      <slot name="header"></slot>
    </div>

    <div
      :class="props.contentClass"
      class="app-card-content"
    >
      <slot></slot>
    </div>

    <div
      v-if="$slots.footer"
      class="app-card-footer"
    >
      <slot name="footer"></slot>
    </div>

    <slot name="root"></slot>
  </div>
</template>

<script setup lang="ts">
  import { computed } from "vue";

  /**
   * The standard card surface. Every card in the product is an `AppCard`, never a bare `v-card`
   * (CLAUDE.md §3.5).
   *
   * Title goes in the `header` slot, actions in the `footer` slot, body in the default slot. The
   * header and footer bars render only when their slot is filled, so a plain card passes nothing —
   * there is no `header`/`footer` prop to remember.
   *
   * Styling lives in `assets/styles/imports/_cards.scss`, not in a scoped block: the card is a
   * recurring surface, which is step 2 of the styling ladder (CLAUDE.md §3.5).
   */
  const props = withDefaults(
    defineProps<{
      /** Dense header/footer bars — for dialogs and side panels, where 55px is too tall. */
      slim?: boolean;
      /** Adds a shadow. Only for genuinely floating surfaces; the default card is flat. */
      elevated?: boolean;
      /** Marks the card as clickable: pointer cursor and a hover tint. */
      selectable?: boolean;
      /** Renders the selected state. Pairs with `selectable`. */
      selected?: boolean;
      /** Classes for the scrolling content area — Vuetify spacing utilities, typically `pa-4`. */
      contentClass?: string;
    }>(),
    {
      slim: false,
      elevated: false,
      selectable: false,
      selected: false,
      contentClass: "pa-4",
    },
  );

  const emit = defineEmits<{
    click: [event: MouseEvent];
  }>();

  const rootClasses = computed(() => ({
    slim: props.slim,
    elevated: props.elevated,
    selectable: props.selectable,
    selected: props.selected,
  }));

  /** Emits only when the card is actually interactive, so a static card raises no stray events. */
  const onClick = (event: MouseEvent): void => {
    if (!props.selectable) return;
    emit("click", event);
  };
</script>
