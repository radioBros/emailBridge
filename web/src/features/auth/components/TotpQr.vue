<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import QRCode from "qrcode";

const props = defineProps<{
  uri: string;
}>();

const dataUrl = ref<string>("");

const render = async (): Promise<void> => {
  if (!props.uri) {
    dataUrl.value = "";
    return;
  }
  dataUrl.value = await QRCode.toDataURL(props.uri, {
    margin: 1,
    width: 220,
    errorCorrectionLevel: "M",
    color: { dark: "#121933", light: "#ffffff" },
  });
};

onMounted(() => {
  void render();
});
watch(
  () => props.uri,
  () => {
    void render();
  },
);
</script>

<template>
  <div class="d-flex justify-center">
    <img
      v-if="dataUrl"
      :src="dataUrl"
      alt=""
      width="220"
      height="220"
      class="auth-qr"
    />
  </div>
</template>

<style scoped lang="scss">
.auth-qr {
  border-radius: 12px;
  border: 1px solid rgb(var(--v-theme-outline) / 24%);
}
</style>
