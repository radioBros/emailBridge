<script setup lang="ts">
/**
 * Visual payload editor: the delivered body as a tree of keys → values. The tree is owned by the editor
 * (`root`, null = no map: the endpoint's normal payload) and edited in place; `change` reports edits,
 * `update:root` a replaced root (start from …, clear).
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiCodeBraces,
  mdiDatabaseOutline,
  mdiEmailOutline,
  mdiFeather,
  mdiRestore,
} from "@mdi/js";
import { startingMap, type MapNode, type MapStart } from "@/features/rules/model/mapTree";
import MapBody from "@/features/rules/components/MapBody.vue";
import MapKindValue from "@/features/rules/components/MapKindValue.vue";
import { createMapTree, provideMapTree } from "@/features/rules/composables/useMapTree";

const props = defineProps<{
  root: MapNode | null;
  eventTypes: string[];
  secretNames: string[];
}>();

const emit = defineEmits<{
  change: [];
  "update:root": [node: MapNode | null];
}>();

const { t } = useI18n();

provideMapTree(
  createMapTree(
    () => emit("change"),
    (node) => emit("update:root", node),
  ),
);

const starts = computed(() => [
  { id: "event" as MapStart, icon: mdiEmailOutline, color: "primary" },
  { id: "data" as MapStart, icon: mdiDatabaseOutline, color: "secondary" },
  { id: "slim" as MapStart, icon: mdiFeather, color: "accent" },
  { id: "empty" as MapStart, icon: mdiCodeBraces, color: "info" },
]);

const nested = computed(() => props.root && (props.root.kind === "object" || props.root.kind === "list"));

const start = (s: MapStart): void => {
  emit("update:root", startingMap(s));
  emit("change");
};

const clear = (): void => {
  emit("update:root", null);
  emit("change");
};
</script>

<template>
  <div class="d-flex flex-column ga-3">
    <div
      v-if="!root"
      class="d-flex flex-column ga-3 py-2"
      data-testid="map-empty"
    >
      <p class="text-body-2 text-medium-emphasis mb-0">
        {{ t("rules.map.none") }}
      </p>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          v-for="s in starts"
          :key="s.id"
          :color="s.color"
          :prepend-icon="s.icon"
          :data-testid="`map-start-${s.id}`"
          @click="start(s.id)"
        >
          {{ t(`rules.map.start_${s.id}`) }}
        </v-btn>
      </div>
    </div>

    <template v-else>
      <div class="d-flex flex-wrap align-center ga-2">
        <span class="text-body-2 text-medium-emphasis">{{ t("rules.map.start_over") }}</span>
        <v-btn-inline
          v-for="s in starts"
          :key="s.id"
          :color="s.color"
          :prepend-icon="s.icon"
          @click="start(s.id)"
        >
          {{ t(`rules.map.start_${s.id}`) }}
        </v-btn-inline>
        <v-spacer />
        <v-btn-inline
          :prepend-icon="mdiRestore"
          color="error"
          data-testid="map-clear"
          @click="clear"
        >
          {{ t("rules.map.clear") }}
        </v-btn-inline>
      </div>

      <MapBody
        v-if="nested"
        :node="root as Extract<MapNode, { kind: 'object' | 'list' }>"
        :event-types="eventTypes"
        :secret-names="secretNames"
      />
      <div
        v-else
        class="d-flex"
      >
        <MapKindValue
          :node="root"
          :event-types="eventTypes"
          :secret-names="secretNames"
          @replace="(n) => { emit('update:root', n); emit('change'); }"
        />
      </div>
    </template>
  </div>
</template>
