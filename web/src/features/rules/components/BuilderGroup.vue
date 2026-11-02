<script setup lang="ts">
/** A group of conditions joined by all / any / not, nestable up to the backend's 8 levels. */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiArrowDown,
  mdiArrowUp,
  mdiDeleteOutline,
  mdiFolderPlusOutline,
  mdiPlus,
  mdiUngroup,
} from "@mdi/js";
import type { RuleGroup, RuleMatch } from "@/api/rules";
import { actionOf } from "@/constants/Actions";
import { isGroup, keyOf, newLeaf } from "@/features/rules/model/builder";
import BuilderCondition from "@/features/rules/components/BuilderCondition.vue";
import { useBuilderTree } from "@/features/rules/composables/useBuilderTree";

const props = withDefaults(
  defineProps<{
    group: RuleGroup;
    depth?: number;
    parent?: RuleGroup | null;
    index?: number;
    eventTypes: string[];
  }>(),
  { depth: 1, parent: null, index: 0 },
);

const { t } = useI18n();
const tree = useBuilderTree();
const createAction = actionOf("create");

const isRoot = computed(() => !props.parent);
const joiner = computed(() => (props.group.match === "any" ? t("rules.builder.or") : t("rules.builder.and")));
const canNest = computed(() => tree.canNest(props.depth + 1));

const matchItems = computed(() =>
  (["all", "any", "not"] as RuleMatch[]).map((m) => ({ value: m, title: t(`rules.builder.match_${m}`) })),
);

const setMatch = (m: RuleMatch | null): void => {
  if (m) tree.patch(props.group, { match: m });
};

const addCondition = (): void => {
  tree.add(props.group, newLeaf(props.group.conditions.length ? "subject" : "type"));
};
</script>

<template>
  <div
    :class="{ 'rule-group--nested pa-3': !isRoot, 'rule-group--not': group.match === 'not' }"
    :data-testid="isRoot ? 'rule-builder-root' : `rule-group-${depth}`"
    :aria-label="t('rules.builder.group_label', { match: t(`rules.builder.match_${group.match}`) })"
    class="d-flex flex-column ga-1"
    role="group"
  >
    <div class="d-flex flex-wrap align-center ga-2 mb-1">
      <v-btn-toggle
        :model-value="group.match"
        :aria-label="t('rules.builder.match')"
        color="primary"
        data-testid="rule-group-match"
        mandatory
        divided
        @update:model-value="setMatch"
      >
        <v-btn
          v-for="m in matchItems"
          :key="m.value"
          :value="m.value"
        >
          {{ m.title }}
        </v-btn>
      </v-btn-toggle>
      <span class="text-body-2 text-medium-emphasis">
        {{ t(`rules.builder.match_${group.match}_hint`) }}
      </span>
      <v-spacer />
      <template v-if="!isRoot && parent">
        <v-btn-inline
          :icon="mdiArrowUp"
          :disabled="index === 0"
          :aria-label="t('rules.builder.move_up')"
          @click="tree.move(parent, index, -1)"
        />
        <v-btn-inline
          :icon="mdiArrowDown"
          :disabled="index === parent.conditions.length - 1"
          :aria-label="t('rules.builder.move_down')"
          @click="tree.move(parent, index, 1)"
        />
        <v-btn-inline
          v-if="group.match !== 'not'"
          v-tippy="t('rules.builder.ungroup')"
          :icon="mdiUngroup"
          :aria-label="t('rules.builder.ungroup')"
          color="secondary"
          @click="tree.unwrap(parent, index)"
        />
        <v-btn-inline
          :icon="mdiDeleteOutline"
          :aria-label="t('rules.builder.remove_group')"
          color="error"
          @click="tree.remove(parent, index)"
        />
      </template>
    </div>

    <div
      v-if="!group.conditions.length"
      class="text-body-2 text-medium-emphasis py-3 px-1"
    >
      {{ isRoot ? t("rules.builder.empty_root") : t("rules.builder.empty_group") }}
    </div>

    <template
      v-for="(c, i) in group.conditions"
      :key="keyOf(c)"
    >
      <div
        v-if="i > 0"
        class="d-flex align-center ga-2 px-1"
        aria-hidden="true"
      >
        <v-chip
          :color="group.match === 'any' ? 'secondary' : 'primary'"
          class="text-uppercase font-weight-bold rule-joiner"
          label
        >
          {{ joiner }}
        </v-chip>
        <v-divider />
      </div>
      <BuilderGroup
        v-if="isGroup(c)"
        :group="c"
        :parent="group"
        :index="i"
        :depth="depth + 1"
        :event-types="eventTypes"
      />
      <BuilderCondition
        v-else
        :leaf="c"
        :group="group"
        :index="i"
        :depth="depth"
        :event-types="eventTypes"
      />
    </template>

    <div class="d-flex flex-wrap ga-2 mt-2">
      <v-btn-inline
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        data-testid="rule-add-condition"
        @click="addCondition"
      >
        {{ t("rules.builder.add_condition") }}
      </v-btn-inline>
      <v-btn-inline
        :disabled="!canNest"
        :prepend-icon="mdiFolderPlusOutline"
        color="secondary"
        data-testid="rule-add-group"
        @click="tree.addGroup(group, group.match === 'any' ? 'all' : 'any')"
      >
        {{ t("rules.builder.add_group") }}
      </v-btn-inline>
    </div>
  </div>
</template>

<style scoped lang="scss">
.rule-group--nested {
  border: 1px solid rgb(var(--v-theme-secondary), 0.28);
  border-radius: 14px;
  background: rgb(var(--v-theme-secondary), 0.04);
}

.rule-group--not {
  border-color: rgb(var(--v-theme-error), 0.3);
  background: rgb(var(--v-theme-error), 0.035);
}

.rule-joiner {
  letter-spacing: 0.06em;
}
</style>
