<script setup lang="ts">
/** One condition: field → operator → value, every part a dropdown that fits the field. */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiArrowDown, mdiArrowUp, mdiDeleteOutline, mdiDotsVertical, mdiFolderPlusOutline } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { RuleGroup, RuleLeaf } from "@/api/rules";
import {
  defOf,
  folderLeafFor,
  folderViewOf,
  newLeaf,
  opNeedsValue,
  valueForOp,
} from "@/features/rules/model/builder";
import { opLabel } from "@/features/rules/components/labels";
import FieldSelect from "@/features/rules/components/FieldSelect.vue";
import TimezoneSelect from "@/features/rules/components/TimezoneSelect.vue";
import ValueInput from "@/features/rules/components/ValueInput.vue";
import { useBuilderTree } from "@/features/rules/composables/useBuilderTree";

const props = defineProps<{
  leaf: RuleLeaf;
  group: RuleGroup;
  index: number;
  depth: number;
  eventTypes: string[];
}>();

const { t } = useI18n();
const tree = useBuilderTree();

const isFolder = computed(() => props.leaf.field === "folder" || props.leaf.field === "isInbox");
const def = computed(() => defOf(isFolder.value ? "folder" : props.leaf.field, props.eventTypes));
const view = computed(() => (isFolder.value ? folderViewOf(props.leaf) : { op: props.leaf.op, value: props.leaf.value }));
const showOp = computed(() => def.value.ops.length > 1);
const showValue = computed(() => opNeedsValue(view.value.op) && def.value.value !== "none");

const opItems = computed(() => def.value.ops.map((op) => ({ title: opLabel(t, op), value: op })));

const setField = (field: string): void => {
  if (!field || field === props.leaf.field) return;
  tree.replace(props.group, props.index, newLeaf(field, props.eventTypes));
};

const setOp = (op: string): void => {
  if (!op) return;
  if (isFolder.value) {
    tree.replace(props.group, props.index, folderLeafFor(op, view.value.value));
    return;
  }
  tree.patch(props.leaf, { op, value: valueForOp(props.leaf, op) });
};

const setValue = (value: unknown): void => {
  if (isFolder.value) {
    const next = folderLeafFor(view.value.op, value);
    if (next.field !== props.leaf.field) tree.replace(props.group, props.index, next);
    else tree.patch(props.leaf, { op: next.op, value: next.value });
    return;
  }
  tree.patch(props.leaf, { value });
};

const setName = (name: string): void => tree.patch(props.leaf, { name });

const headerItems = [
  "list-id",
  "list-unsubscribe",
  "auto-submitted",
  "precedence",
  "x-mailer",
  "x-priority",
  "reply-to",
  "return-path",
  "delivered-to",
  "x-original-to",
  "x-spam-flag",
  "x-ms-exchange-organization-scl",
];

const isFirst = computed(() => props.index === 0);
const isLast = computed(() => props.index === props.group.conditions.length - 1);
</script>

<template>
  <div
    :data-testid="`rule-condition-${index}`"
    class="rule-condition d-flex align-start pa-1"
  >
    <v-row class="ma-0 flex-grow-1 min-w-0">
      <v-col
        :xl="def.name ? 3 : 4"
        cols="12"
        sm="6"
        class="pa-1"
      >
        <FieldSelect
          :model-value="leaf.field"
          :event-types="eventTypes"
          @update:model-value="setField"
        />
      </v-col>
      <v-col
        v-if="def.name === 'header'"
        cols="12"
        sm="6"
        xl="3"
        class="pa-1"
      >
        <v-combobox
          :model-value="leaf.name ?? ''"
          :items="headerItems"
          :label="t('rules.builder.header_name')"
          class="no-validation"
          hide-details
          @update:model-value="(v) => setName(String(v ?? '').toLowerCase())"
        />
      </v-col>
      <v-col
        v-if="def.name === 'tz'"
        cols="12"
        sm="6"
        xl="3"
        class="pa-1"
      >
        <TimezoneSelect
          :model-value="leaf.name ?? 'UTC'"
          @update:model-value="setName"
        />
      </v-col>
      <v-col
        v-if="showOp"
        :xl="def.name ? 2 : 3"
        cols="12"
        sm="6"
        class="pa-1"
      >
        <v-select
          v-if="opItems.length <= 4"
          :model-value="view.op"
          :items="opItems"
          :label="t('rules.builder.op')"
          class="no-validation"
          data-testid="rule-op"
          hide-details
          @update:model-value="setOp"
        />
        <AutocompleteSearch
          v-else
          :model-value="view.op"
          :items="opItems"
          :label="t('rules.builder.op')"
          class="no-validation"
          data-testid="rule-op"
          hide-details
          @update:model-value="(v) => setOp(String(v ?? ''))"
        />
      </v-col>
      <v-col
        v-if="showValue || def.value === 'list'"
        :sm="def.name || !showOp ? 6 : 12"
        :xl="(def.name ? 4 : 5) + (showOp ? 0 : 3)"
        cols="12"
        class="pa-1"
      >
        <ValueInput
          :leaf="leaf"
          :def="def"
          :op="view.op"
          :value="view.value"
          @value="setValue"
          @name="setName"
        />
      </v-col>
    </v-row>
    <div class="d-flex align-center ga-1 pt-2 flex-shrink-0">
      <v-menu location="bottom end">
        <template #activator="{ props: menuProps }">
          <v-btn-inline
            :icon="mdiDotsVertical"
            :aria-label="t('rules.builder.more')"
            data-testid="rule-condition-menu"
            v-bind="menuProps"
          />
        </template>
        <v-list>
          <v-list-item
            :disabled="isFirst"
            :prepend-icon="mdiArrowUp"
            :title="t('rules.builder.move_up')"
            @click="tree.move(group, index, -1)"
          />
          <v-list-item
            :disabled="isLast"
            :prepend-icon="mdiArrowDown"
            :title="t('rules.builder.move_down')"
            @click="tree.move(group, index, 1)"
          />
          <v-list-item
            :disabled="!tree.canNest(depth + 1)"
            :prepend-icon="mdiFolderPlusOutline"
            :title="t('rules.builder.wrap')"
            @click="tree.wrap(group, index)"
          />
        </v-list>
      </v-menu>
      <v-btn-inline
        :icon="mdiDeleteOutline"
        :aria-label="t('rules.builder.remove')"
        color="error"
        data-testid="rule-condition-remove"
        @click="tree.remove(group, index)"
      />
    </div>
  </div>
</template>

<style scoped lang="scss">
.rule-condition {
  border-radius: 12px;
  transition: background-color 160ms ease;

  &:focus-within {
    background: rgb(var(--v-theme-primary), 0.04);
  }
}
</style>
