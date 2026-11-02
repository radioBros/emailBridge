<template>
  <div
    :aria-label="t('common.states.loading')"
    aria-busy="true"
    role="status"
  >
    <v-skeleton-loader
      :type="skeletonType"
      class="w-100"
    />
  </div>
</template>

<script setup lang="ts">
  import { computed } from "vue";
  import { useI18n } from "vue-i18n";

  /**
   * A shape-matched loading placeholder.
   *
   * **Shape-matched is the whole point.** A spinner in the middle of a page tells the user nothing
   * and makes the layout jump when content lands; a skeleton the same shape as what is coming keeps
   * the page still and sets an expectation. So `shape` names *what is loading*, and this maps it
   * to Vuetify's skeleton grammar in one place rather than each screen inventing a type string.
   *
   * `role="status"` with `aria-busy` is what tells a screen reader something is in flight — without
   * it the page simply appears empty, which is indistinguishable from having no data.
   */
  // Named `shape`, not `variant`: `variant` is reserved for the Vuetify styling attribute that
  // comes from `defaults` (README §4). This prop chooses *what* is skeletoned — a different
  // question, and one the caller genuinely owns.
  const props = withDefaults(
    defineProps<{
      shape?: "table" | "card" | "calendar" | "list" | "form" | "chart" | "kpi" | "tile";
      /** Rows for the shapes that repeat. Ignored by `card`, `calendar`, `kpi`, `tile`. */
      rows?: number;
    }>(),
    { shape: "card", rows: 5 },
  );

  const { t } = useI18n();

  const skeletonType = computed(() => {
    switch (props.shape) {
      case "table":
        return `table-heading, ${Array.from({ length: props.rows }, () => "table-row").join(", ")}`;
      case "calendar":
        // Six week-rows is the worst case for a month grid, so the placeholder never grows when the
        // real calendar arrives.
        return Array.from({ length: 6 }, () => "list-item").join(", ");
      case "list":
        return Array.from({ length: props.rows }, () => "list-item-two-line").join(", ");
      case "form":
        return Array.from({ length: props.rows }, () => "heading, text").join(", ");
      case "chart":
        // An image block, not text lines: a chart resolves into one large rectangle, and a
        // paragraph-shaped placeholder would make the card visibly change shape when it lands.
        return "image";
      case "kpi":
        // Matches dash-kpi: label row + big number + optional hint (~145 px, not a tall card).
        return "text, heading, text";
      case "tile":
        // Matches compact side tiles: avatar + two text lines in one row.
        return "list-item-avatar-two-line";
      case "card":
      default:
        return "heading, paragraph, actions";
    }
  });
</script>
