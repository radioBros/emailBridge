<script setup lang="ts">
import { mdiShieldLockOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";

defineProps<{
  title: string;
  body: string;
}>();
</script>

<template>
  <AppCard>
    <div class="d-flex flex-column align-center text-center py-8 px-4 ga-3">
      <v-avatar-hero
        color="warning"
      >
        <v-icon
          :icon="mdiShieldLockOutline"
        />
      </v-avatar-hero>
      <div class="text-title-medium font-weight-bold">
        {{ title }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ body }}
      </p>
    </div>
  </AppCard>
</template>
