<script setup lang="ts">
import { mdiFilterVariant, mdiDotsVertical, mdiHistory } from "@mdi/js";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import type { Route } from "@/api/routes";

defineProps<{
  item: Route;
}>();

const emit = defineEmits<{
  edit: [];
  test: [];
  versions: [];
  toggle: [];
  delete: [];
}>();

const { t } = useI18n();
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
const testAction = actionOf("test");
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
      :aria-label="t('routes.edit')"
      @click="emit('edit')"
    />
    <v-btn
      :color="testAction.color"
      :icon="testAction.icon"
      :aria-label="t('routes.test')"
      @click="emit('test')"
    />
    <v-btn
      v-tippy="t('rules.open_editor')"
      :to="`/routes/${item.id}/filter`"
      :icon="mdiFilterVariant"
      :aria-label="t('rules.open_editor')"
      :data-testid="`open-rule-${item.id}`"
      color="accent"
    />
    <v-btn
      :icon="mdiHistory"
      :aria-label="t('routes.versions')"
      color="accent"
      @click="emit('versions')"
    />
    <v-menu location="bottom end">
      <template #activator="{ props: menuProps }">
        <v-btn
          v-bind="menuProps"
          :icon="mdiDotsVertical"
          :aria-label="t('routes.more')"
        />
      </template>
      <v-list>
        <v-list-item
          :prepend-icon="item.enabled ? disableAction.icon : enableAction.icon"
          :title="item.enabled ? t('routes.disable') : t('routes.enable')"
          @click="emit('toggle')"
        />
        <v-list-item
          :prepend-icon="deleteAction.icon"
          :title="t('routes.delete')"
          base-color="error"
          @click="emit('delete')"
        />
      </v-list>
    </v-menu>
  </div>
</template>
