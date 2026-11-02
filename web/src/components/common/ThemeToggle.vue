<script setup lang="ts">
import { CHROME_ICON_COLOR } from "@/core/config/IconColors";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useTheme } from "vuetify";
import { mdiWeatherNight, mdiWhiteBalanceSunny } from "@mdi/js";
import { useThemePreference } from "@/composables/useThemePreference";

const { t } = useI18n();
const theme = useTheme();
const { toggleLightDark } = useThemePreference();

const isDark = computed(() => theme.global.current.value.dark);
const icon = computed(() => (isDark.value ? mdiWhiteBalanceSunny : mdiWeatherNight));
const label = computed(() => t("shell.theme_toggle"));
</script>

<template>
  <v-btn
    v-tippy="label"
    :icon="icon"
    :color="CHROME_ICON_COLOR.theme"
    :aria-label="label"
    data-testid="theme-toggle"
    @click="toggleLightDark"
  />
</template>
