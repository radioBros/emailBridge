<script setup lang="ts">
import { onMounted } from "vue";
import { useLocale } from "vuetify";
import UpdateMgr from "@/components/UpdateMgr.vue";
import { bindVuetifyLocale } from "@/app/i18n";

onMounted(() => {
  const { current } = useLocale();
  bindVuetifyLocale({ current });
});
</script>

<template>
  <router-view />
  <UpdateMgr />
</template>
