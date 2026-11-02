<script setup lang="ts">
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import NotifyHost from "@/components/common/NotifyHost.vue";
import { useThemePreference } from "@/composables/useThemePreference";

const { followSystemWhileMounted } = useThemePreference();
followSystemWhileMounted();
</script>

<template>
  <v-app>
    <v-main class="auth-layout d-flex align-center justify-center">
      <v-container class="py-8">
        <v-row justify="center">
          <v-col
            cols="12"
            sm="10"
            md="7"
            lg="5"
          >
            <router-view />
          </v-col>
        </v-row>
      </v-container>
    </v-main>
    <ConfirmDialog />
    <NotifyHost />
  </v-app>
</template>

<style scoped lang="scss">
.auth-layout {
  min-height: 100dvh;
  background:
    radial-gradient(ellipse at 20% 20%, rgb(var(--v-theme-primary) / 14%), transparent 50%),
    radial-gradient(ellipse at 80% 0%, rgb(var(--v-theme-accent) / 12%), transparent 45%),
    radial-gradient(ellipse at 50% 100%, rgb(var(--v-theme-secondary) / 10%), transparent 50%),
    rgb(var(--v-theme-background));
}
</style>
