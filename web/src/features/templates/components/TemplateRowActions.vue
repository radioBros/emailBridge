<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import type { TemplateView } from "@/api/templates";

defineProps<{
  item: TemplateView;
}>();

const emit = defineEmits<{
  edit: [];
  delete: [];
}>();

const { t } = useI18n();
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
</script>

<template>
  <div
    class="d-flex flex-nowrap align-center justify-end ga-1"
    @click.stop
  >
    <v-btn
      :color="editAction.color"
      :icon="editAction.icon"
      :aria-label="t('templates.edit')"
      @click="emit('edit')"
    />
    <v-btn
      :color="deleteAction.color"
      :icon="deleteAction.icon"
      :aria-label="t('templates.delete')"
      @click="emit('delete')"
    />
  </div>
</template>
