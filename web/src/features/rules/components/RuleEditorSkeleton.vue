<script setup lang="ts">
/** First-load shape of the editor: the same row of cards, skeletons inside them. */
import AppCard from "@/components/cards/AppCard.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
</script>

<template>
  <v-row>
    <v-col
      cols="12"
      lg="7"
      class="d-flex flex-column ga-4"
    >
      <AppCard class="rule-card h-auto">
        <LoadingSkeleton
          :rows="3"
          shape="form"
        />
      </AppCard>
      <AppCard class="rule-card h-auto">
        <LoadingSkeleton
          :rows="3"
          shape="form"
        />
      </AppCard>
    </v-col>
    <v-col
      cols="12"
      lg="5"
    >
      <AppCard class="rule-card">
        <LoadingSkeleton
          :rows="4"
          shape="list"
        />
      </AppCard>
    </v-col>
  </v-row>
</template>

<style scoped lang="scss">
.rule-card {
  height: auto;
  flex-shrink: 0;
}
</style>
