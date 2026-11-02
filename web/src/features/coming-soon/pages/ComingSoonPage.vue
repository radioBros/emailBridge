<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { mdiClockOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";

defineProps<{
  feature?: string;
}>();

const { t } = useI18n();
</script>

<template>
  <AppCard>
    <div class="d-flex flex-column align-center text-center ga-4 py-8">
      <v-avatar-hero
        color="primary"
      >
        <v-icon
          :icon="mdiClockOutline"
        />
      </v-avatar-hero>
      <div>
        <h2 class="text-h6 font-weight-bold mb-2">
          {{ t("shell.coming_soon_title") }}
        </h2>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("shell.coming_soon_body") }}
        </p>
      </div>
    </div>
  </AppCard>
</template>
