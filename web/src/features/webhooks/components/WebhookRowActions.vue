<script setup lang="ts">
import { mdiFilterVariant, mdiDotsVertical } from "@mdi/js";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import type { EndpointView } from "@/api/webhooks";

defineProps<{
  item: EndpointView;
}>();

const emit = defineEmits<{
  edit: [];
  test: [];
  rotate: [];
  toggle: [];
  delete: [];
}>();

const { t } = useI18n();
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
const testAction = actionOf("test");
const rotateAction = actionOf("rotateSecret");
const enableAction = actionOf("enable");
const disableAction = actionOf("disable");
</script>

<template>
  <div
    class="d-flex flex-nowrap align-center justify-end ga-1"
    @click.stop
  >
    <v-btn
      :color="editAction.color"
      :icon="editAction.icon"
      :aria-label="t('webhooks.edit')"
      @click="emit('edit')"
    />
    <v-btn
      :color="testAction.color"
      :icon="testAction.icon"
      :aria-label="t('webhooks.test')"
      @click="emit('test')"
    />
    <v-btn
      v-tippy="t('rules.open_editor')"
      :to="`/webhooks/${item.id}/filter`"
      :icon="mdiFilterVariant"
      :aria-label="t('rules.open_editor')"
      :data-testid="`open-rule-${item.id}`"
      color="accent"
    />
    <v-menu location="bottom end">
      <template #activator="{ props: menuProps }">
        <v-btn
          v-bind="menuProps"
          :icon="mdiDotsVertical"
          :aria-label="t('webhooks.more')"
        />
      </template>
      <v-list>
        <v-list-item
          :prepend-icon="rotateAction.icon"
          :title="t('webhooks.rotate')"
          @click="emit('rotate')"
        />
        <v-list-item
          :prepend-icon="item.enabled ? disableAction.icon : enableAction.icon"
          :title="item.enabled ? t('webhooks.disable') : t('webhooks.enable')"
          @click="emit('toggle')"
        />
        <v-list-item
          :prepend-icon="deleteAction.icon"
          :title="t('webhooks.delete')"
          base-color="error"
          @click="emit('delete')"
        />
      </v-list>
    </v-menu>
  </div>
</template>
