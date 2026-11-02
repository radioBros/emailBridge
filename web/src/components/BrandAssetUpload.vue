<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCloudUploadOutline, mdiDeleteOutline, mdiImageOutline } from "@mdi/js";
import { actionOf } from "@/constants/Actions";
import {
  uploadBrandingAsset,
  deleteBrandingAsset,
  type BrandingAsset,
  type BrandingAssetKind,
} from "@/api/connect";
import { ApiProblemError } from "@/api/client";
import { useConfirm } from "@/composables/useConfirm";

const props = withDefaults(
  defineProps<{
    kind: BrandingAssetKind;
    assetId?: string | null;
    previewUrl?: string | null;
    label?: string;
    hint?: string;
    accept?: string;
    /** When true (default for gallery), DELETE the asset. Logo slots unlink only until save. */
    deleteOnRemove?: boolean;
  }>(),
  {
    assetId: null,
    previewUrl: null,
    label: "",
    hint: "",
    accept: "image/png,image/jpeg,image/webp,image/gif,image/x-icon,.ico",
    deleteOnRemove: true,
  },
);

const emit = defineEmits<{
  "update:assetId": [string | null];
  "update:previewUrl": [string | null];
  uploaded: [BrandingAsset];
  removed: [];
}>();

const { t } = useI18n();
const deleteAction = actionOf("delete");
const inputRef = ref<HTMLInputElement | null>(null);
const dragging = ref(false);
const uploading = ref(false);
const progress = ref(0);
const error = ref<string | null>(null);
const localPreview = ref<string | null>(null);

const shownUrl = computed(() => localPreview.value || props.previewUrl || null);

const openPicker = (): void => {
  inputRef.value?.click();
};

const onFiles = async (files: FileList | null): Promise<void> => {
  const file = files?.[0];
  if (!file) return;
  error.value = null;
  uploading.value = true;
  progress.value = 15;
  const tick = window.setInterval(() => {
    if (progress.value < 85) progress.value += 10;
  }, 200);
  try {
    const asset = await uploadBrandingAsset(props.kind, file);
    progress.value = 100;
    const url = asset.url ?? "";
    localPreview.value = url;
    emit("update:assetId", asset.id);
    emit("update:previewUrl", url);
    emit("uploaded", { ...asset, url });
  } catch (err) {
    error.value =
      err instanceof ApiProblemError
        ? err.problem.title || err.message
        : t("connect.branding.upload_error");
  } finally {
    window.clearInterval(tick);
    uploading.value = false;
    progress.value = 0;
    if (inputRef.value) inputRef.value.value = "";
  }
};

const onDrop = (ev: DragEvent): void => {
  dragging.value = false;
  void onFiles(ev.dataTransfer?.files ?? null);
};

const confirm = useConfirm();

// Deleting an uploaded asset is irreversible, so it asks first; unlinking a logo slot (saved later) does not.
const onRemove = async (): Promise<void> => {
  error.value = null;
  if (props.deleteOnRemove && props.assetId) {
    const ok = await confirm({
      title: t("connect.branding.remove_title"),
      body: t("connect.branding.remove_body"),
      variant: "danger",
    });
    if (!ok) return;
    try {
      await deleteBrandingAsset(props.assetId);
    } catch (err) {
      error.value =
        err instanceof ApiProblemError
          ? err.problem.title || err.message
          : t("connect.branding.remove_error");
      return;
    }
  }
  localPreview.value = null;
  emit("update:assetId", null);
  emit("update:previewUrl", null);
  emit("removed");
};
</script>

<template>
  <div class="brand-asset-upload">
    <div
      v-if="label"
      class="text-body-medium font-weight-medium mb-1"
    >
      {{ label }}
    </div>
    <p
      v-if="hint"
      class="text-body-small text-medium-emphasis mb-2"
    >
      {{ hint }}
    </p>
    <div class="d-flex flex-wrap ga-3 align-stretch">
      <div
        :aria-label="t('connect.branding.preview_light_dark')"
        class="preview-pair d-flex ga-2"
        role="img"
      >
        <div class="preview-tile preview-tile--light">
          <img
            v-if="shownUrl"
            :src="shownUrl"
            alt=""
            class="preview-img"
          >
          <v-icon
            v-else
            :icon="mdiImageOutline"
            color="medium-emphasis"
          />
        </div>
        <div class="preview-tile preview-tile--dark">
          <img
            v-if="shownUrl"
            :src="shownUrl"
            alt=""
            class="preview-img"
          >
          <v-icon
            v-else
            :icon="mdiImageOutline"
            color="medium-emphasis"
          />
        </div>
      </div>
      <div
        :class="{ 'dropzone--active': dragging, 'dropzone--busy': uploading }"
        :aria-label="t('connect.branding.upload_drop')"
        class="dropzone flex-grow-1"
        role="button"
        tabindex="0"
        @dragenter.prevent="dragging = true"
        @dragover.prevent="dragging = true"
        @dragleave.prevent="dragging = false"
        @drop.prevent="onDrop"
        @click="openPicker"
        @keydown.enter.prevent="openPicker"
        @keydown.space.prevent="openPicker"
      >
        <v-icon
          :icon="mdiCloudUploadOutline"
          color="primary"
          class="mb-1"
        />
        <div class="text-body-small">
          {{ t("connect.branding.upload_drop") }}
        </div>
        <v-progress-linear
          v-if="uploading"
          :model-value="progress"
          class="mt-3"
          color="primary"
          height="4"
          rounded
        />
      </div>
    </div>
    <div class="d-flex flex-wrap ga-2 mt-2">
      <v-btn
        :prepend-icon="mdiCloudUploadOutline"
        :loading="uploading"
        color="primary"
        @click="openPicker"
      >
        {{ t("connect.branding.upload_button") }}
      </v-btn>
      <v-btn
        v-if="assetId || shownUrl"
        :color="deleteAction.color"
        :prepend-icon="mdiDeleteOutline"
        :disabled="uploading"
        @click="onRemove"
      >
        {{ t("connect.branding.remove") }}
      </v-btn>
    </div>
    <v-alert
      v-if="error"
      class="mt-2"
      type="error"
      role="alert"
    >
      {{ error }}
    </v-alert>
    <input
      ref="inputRef"
      :accept="accept"
      class="d-none"
      type="file"
      @change="onFiles(($event.target as HTMLInputElement).files)"
    >
  </div>
</template>

<style lang="scss" scoped>
.preview-tile {
  width: 72px;
  height: 72px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));

  &--light {
    background: #f5f6fb;
  }

  &--dark {
    background: #121933;
  }
}

.preview-img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}

.dropzone {
  border: 1px dashed rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  padding: 1rem;
  min-height: 72px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  transition: border-color 160ms ease-out, background-color 160ms ease-out;

  &:hover,
  &--active {
    border-color: rgb(var(--v-theme-primary));
    background: rgb(var(--v-theme-primary), 0.06);
  }

  &--busy {
    pointer-events: none;
    opacity: 0.85;
  }

  &:focus-visible {
    outline: 2px solid rgb(var(--v-theme-primary));
    outline-offset: 2px;
  }
}
</style>
