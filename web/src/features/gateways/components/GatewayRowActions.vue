<script setup lang="ts">
import { computed } from "vue";
import { mdiDotsVertical } from "@mdi/js";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import type { GatewayView } from "@/api/gateways";
import { isAPIKind } from "@/features/gateways/helpers/labels";

const props = defineProps<{
  item: GatewayView;
}>();

const emit = defineEmits<{
  edit: [];
  verify: [];
  toggle: [];
  delete: [];
}>();

const { t } = useI18n();
const editAction = actionOf("edit");
const verifyAction = actionOf("verify");
const deleteAction = actionOf("delete");
const enableAction = actionOf("enable");
const disableAction = actionOf("disable");
const showVerify = computed(() => !isAPIKind(props.item.kind || "smtp"));
</script>

<template>
  <div
    class="d-flex flex-nowrap align-center justify-end ga-1"
    @click.stop
  >
    <v-btn
      :color="editAction.color"
      :icon="editAction.icon"
      :aria-label="t('gateways.edit')"
      @click="emit('edit')"
    />
    <v-btn
      v-if="showVerify"
      :color="verifyAction.color"
      :icon="verifyAction.icon"
      :aria-label="t('gateways.verify')"
      @click="emit('verify')"
    />
    <v-menu location="bottom end">
      <template #activator="{ props: menuProps }">
        <v-btn
          v-bind="menuProps"
          :icon="mdiDotsVertical"
          :aria-label="t('gateways.more')"
        />
      </template>
      <v-list>
        <v-list-item
          :prepend-icon="item.enabled ? disableAction.icon : enableAction.icon"
          :title="item.enabled ? t('gateways.disable') : t('gateways.enable')"
          @click="emit('toggle')"
        />
        <v-list-item
          :prepend-icon="deleteAction.icon"
          :title="t('gateways.delete')"
          base-color="error"
          @click="emit('delete')"
        />
      </v-list>
    </v-menu>
  </div>
</template>
