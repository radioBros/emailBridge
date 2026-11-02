<template>
  <div
    :aria-label="t('common.states.loading')"
    aria-busy="true"
    role="status"
  >
    <v-row>
      <v-col
        v-for="cell in count"
        :key="cell"
        :cols="cols"
        :lg="lg"
        :md="md"
        :sm="sm"
      >
        <slot>
          <AppCard>
            <v-skeleton-loader type="heading, text, text" />
          </AppCard>
        </slot>
      </v-col>
    </v-row>
  </div>
</template>

<script setup lang="ts">
  import { useI18n } from "vue-i18n";

  // Static, per the static-import carve-out (README §10) — a placeholder that is itself fetched cannot cover the wait.
  import AppCard from "@/components/cards/AppCard.vue";

  /**
   * A first-load placeholder for any screen whose loaded state is a grid of identical cards.
   *
   * **The breakpoints are props because they must match the real grid** (README §10). A
   * skeleton laid out differently from the content it stands in for is worse than none: the page
   * settles into one layout, then jumps into another the moment data lands, which is exactly the
   * jank a skeleton is supposed to prevent. So a caller passes the same `cols`/`sm`/`md`/`lg` it
   * passes its own `v-col`s.
   *
   * **The skeleton goes INSIDE a real `AppCard`, not instead of one.** The card is chrome, not
   * data: its border, radius, padding and position are known before any request is made, so they
   * should be on screen from the first paint with only the *contents* standing in. A skeleton
   * shaped *like* a card still swaps one box for another when data lands — same border redrawn,
   * same shadow recomputed — and that is a flicker the user can see.
   *
   * The cell itself is a **slot**, defaulting to a card holding a text-shaped skeleton (slots over props). A
   * screen whose tiles are a different shape — a chart, a wide row — overrides the slot rather than
   * gaining a `shape` prop, so a third consumer never has to edit this file.
   *
   * For a grid whose cells are *not* identical — the dashboard's three narrow summaries beside two
   * wide charts — this is the wrong tool: write that screen its own skeleton mirroring its own
   * columns.
   */
  withDefaults(
    defineProps<{
      /** How many placeholder cells. Match the page size the screen usually shows. */
      count?: number;
      cols?: string;
      sm?: string;
      md?: string;
      lg?: string;
    }>(),
    { count: 6, cols: "12", sm: "12", md: "6", lg: "4" },
  );

  const { t } = useI18n();
</script>
