<script setup lang="ts">
/** The keys of an object (or the items of a list) in the payload tree, nested as deep as needed. */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiArrowDown, mdiArrowUp, mdiDeleteOutline, mdiDotsVertical, mdiPlus } from "@mdi/js";
import { actionOf } from "@/constants/Actions";
import { keyOf } from "@/features/rules/model/builder";
import { duplicateKeys, type MapNode } from "@/features/rules/model/mapTree";
import MapKindValue from "@/features/rules/components/MapKindValue.vue";
import { useMapTree } from "@/features/rules/composables/useMapTree";

const props = withDefaults(
  defineProps<{
    node: Extract<MapNode, { kind: "object" | "list" }>;
    eventTypes: string[];
    secretNames: string[];
    depth?: number;
  }>(),
  { depth: 0 },
);

const { t } = useI18n();
const ops = useMapTree();
const createAction = actionOf("create");

const dups = computed(() => (props.node.kind === "object" ? duplicateKeys(props.node.entries) : []));

const rows = computed(() =>
  props.node.kind === "object"
    ? props.node.entries.map((e, i) => ({ id: keyOf(e), i, entry: e, child: e.node }))
    : props.node.items.map((n, i) => ({ id: keyOf(n), i, entry: null, child: n })),
);

const count = computed(() => rows.value.length);

const replace = (i: number, next: MapNode): void => {
  if (props.node.kind === "object") ops.setEntryNode(props.node.entries[i]!, next);
  else ops.setItem(props.node, i, next);
};

const move = (i: number, d: number): void => {
  if (props.node.kind === "object") ops.moveEntry(props.node, i, d);
  else ops.moveItem(props.node, i, d);
};

const remove = (i: number): void => {
  if (props.node.kind === "object") ops.removeEntry(props.node, i);
  else ops.removeItem(props.node, i);
};

const add = (): void => {
  if (props.node.kind === "object") ops.addEntry(props.node);
  else ops.addItem(props.node);
};

const isNested = (n: MapNode): n is Extract<MapNode, { kind: "object" | "list" }> =>
  n.kind === "object" || n.kind === "list";
</script>

<template>
  <div
    :class="{ 'map-body--nested ps-3 ps-md-4': depth > 0 }"
    class="d-flex flex-column ga-1"
  >
    <div
      v-for="row in rows"
      :key="row.id"
      :data-testid="`map-row-${depth}-${row.i}`"
      class="map-row d-flex flex-column"
    >
      <div class="d-flex align-start ga-1 pa-1">
        <v-row class="ma-0 flex-grow-1 min-w-0">
          <v-col
            cols="12"
            sm="4"
            xl="3"
            class="pa-1"
          >
            <v-text-field
              v-if="row.entry"
              :model-value="row.entry.key"
              :label="t('rules.map.key')"
              :error="dups.includes(row.entry.key)"
              :error-messages="dups.includes(row.entry.key) ? [t('rules.map.duplicate_key')] : []"
              :hide-details="!dups.includes(row.entry.key)"
              class="no-validation font-mono-input"
              autocomplete="off"
              spellcheck="false"
              @update:model-value="(v) => ops.renameKey(row.entry!, String(v ?? ''))"
            />
            <v-chip
              v-else
              class="mt-3 font-mono"
              label
            >
              [{{ row.i }}]
            </v-chip>
          </v-col>
          <v-col
            cols="12"
            sm="8"
            xl="9"
            class="pa-0 d-flex"
          >
            <MapKindValue
              :node="row.child"
              :event-types="eventTypes"
              :secret-names="secretNames"
              @replace="(n) => replace(row.i, n)"
            />
          </v-col>
        </v-row>
        <div class="d-flex align-center ga-1 pt-2 flex-shrink-0">
          <v-menu location="bottom end">
            <template #activator="{ props: menuProps }">
              <v-btn-inline
                :icon="mdiDotsVertical"
                :aria-label="t('rules.builder.more')"
                v-bind="menuProps"
              />
            </template>
            <v-list>
              <v-list-item
                :disabled="row.i === 0"
                :prepend-icon="mdiArrowUp"
                :title="t('rules.builder.move_up')"
                @click="move(row.i, -1)"
              />
              <v-list-item
                :disabled="row.i === count - 1"
                :prepend-icon="mdiArrowDown"
                :title="t('rules.builder.move_down')"
                @click="move(row.i, 1)"
              />
            </v-list>
          </v-menu>
          <v-btn-inline
            :icon="mdiDeleteOutline"
            :aria-label="t('rules.map.remove')"
            color="error"
            @click="remove(row.i)"
          />
        </div>
      </div>
      <MapBody
        v-if="isNested(row.child)"
        :node="row.child"
        :event-types="eventTypes"
        :secret-names="secretNames"
        :depth="depth + 1"
      />
    </div>

    <div
      v-if="!count"
      class="text-body-2 text-medium-emphasis px-2 py-2"
    >
      {{ node.kind === "object" ? t("rules.map.empty_object") : t("rules.map.empty_list") }}
    </div>

    <div class="px-1 pb-1">
      <v-btn-inline
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        :data-testid="`map-add-${depth}`"
        @click="add"
      >
        {{ node.kind === "object" ? t("rules.map.add_key") : t("rules.map.add_item") }}
      </v-btn-inline>
    </div>
  </div>
</template>

<style scoped lang="scss">
.map-body--nested {
  border-inline-start: 1px dashed rgb(var(--v-theme-secondary), 0.4);
  margin-inline-start: 12px;
}

.map-row {
  border-radius: 12px;
  transition: background-color 160ms ease;

  &:focus-within {
    background: rgb(var(--v-theme-primary), 0.04);
  }
}

.font-mono-input :deep(input) {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
}

.font-mono {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}
</style>
