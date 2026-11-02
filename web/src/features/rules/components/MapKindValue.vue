<script setup lang="ts">
/** What one payload value is (field, constant, secret, helper, expression, object, list) and its input. */
import type { ListOption } from "@/features/rules/components/listItem";
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiCodeBraces,
  mdiCodeBrackets,
  mdiFunctionVariant,
  mdiKeyChainVariant,
  mdiFormatQuoteClose,
  mdiCodeJson,
  mdiSigma,
} from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { allFieldsFor } from "@/features/rules/catalog/catalog";
import { helperKey } from "@/features/rules/model/helpers";
import { SECRET_NAME_RE, VALUE_HELPERS, nodeOfKind, type MapKind, type MapNode } from "@/features/rules/model/mapTree";
import { useMapTree } from "@/features/rules/composables/useMapTree";

const props = defineProps<{
  node: MapNode;
  eventTypes: string[];
  secretNames: string[];
}>();

const emit = defineEmits<{
  replace: [node: MapNode];
}>();

const { t, te } = useI18n();
const ops = useMapTree();

interface KindItem {
  title: string;
  value: MapKind;
  icon: string;
  [key: string]: unknown;
}

const KIND_ICONS: Record<MapKind, string> = {
  field: mdiCodeJson,
  constant: mdiFormatQuoteClose,
  secret: mdiKeyChainVariant,
  helper: mdiFunctionVariant,
  expr: mdiSigma,
  object: mdiCodeBraces,
  list: mdiCodeBrackets,
};

const kindItems = computed((): KindItem[] =>
  (["field", "constant", "secret", "helper", "expr", "object", "list"] as MapKind[]).map((k) => ({
    title: t(`rules.map.kind_${k}`),
    value: k,
    icon: KIND_ICONS[k],
  })),
);

const setKind = (k: unknown): void => {
  const kind = k as MapKind;
  if (!kind || kind === props.node.kind) return;
  emit("replace", nodeOfKind(kind, props.node));
};

const fieldItems = computed(() => [
  { title: "event", value: "event", subtitle: t("rules.map.whole_event") },
  { title: "data", value: "data", subtitle: t("rules.map.whole_data") },
  { title: "type", value: "type", subtitle: t("rules.map.event_type") },
  ...allFieldsFor(props.eventTypes)
    .filter((f) => f.path !== "event.data")
    .map((f) => ({ title: f.path, value: f.path, subtitle: f.description ?? t(`rules.type.${f.type}`) })),
]);

const helperItems = computed(() =>
  VALUE_HELPERS.map((h) => ({ title: `${h}()`, value: h, subtitle: te(helperKey(h)) ? t(helperKey(h)) : "" })),
);

type ConstType = "string" | "number" | "bool" | "null";

const constType = computed((): ConstType => {
  if (props.node.kind !== "constant") return "string";
  const v = props.node.value;
  if (v === null) return "null";
  if (typeof v === "number") return "number";
  if (typeof v === "boolean") return "bool";
  return "string";
});

const constTypeItems = computed(() =>
  (["string", "number", "bool", "null"] as ConstType[]).map((c) => ({ title: t(`rules.map.const_${c}`), value: c })),
);

const setConstType = (c: ConstType): void => {
  const value = c === "null" ? null : c === "number" ? 0 : c === "bool" ? true : "";
  ops.patchNode(props.node, { value });
};

const setConst = (raw: unknown): void => {
  if (constType.value === "number") {
    const n = Number(raw);
    ops.patchNode(props.node, { value: Number.isFinite(n) ? n : 0 });
  } else ops.patchNode(props.node, { value: String(raw ?? "") });
};

const secretRules = [(v: unknown): true | string => SECRET_NAME_RE.test(String(v ?? "")) || t("rules.map.secret_invalid")];
</script>

<template>
  <v-row class="ma-0 flex-grow-1 min-w-0">
    <v-col
      cols="12"
      xl="4"
      class="pa-1"
    >
      <AutocompleteSearch
        :model-value="node.kind"
        :items="kindItems"
        :label="t('rules.map.kind')"
        class="no-validation"
        data-testid="map-kind"
        hide-details
        @update:model-value="setKind"
      >
        <template #item="{ props: itemProps, item }">
          <v-list-item
            :title="(item as KindItem).title"
            :prepend-icon="(item as KindItem).icon"
            v-bind="itemProps"
          />
        </template>
      </AutocompleteSearch>
    </v-col>

    <v-col
      cols="12"
      xl="8"
      class="pa-1 d-flex flex-column ga-2 min-w-0"
    >
      <AutocompleteSearch
        v-if="node.kind === 'field'"
        :model-value="node.path"
        :items="fieldItems"
        :label="t('rules.map.field')"
        :search-fields="['subtitle']"
        class="no-validation"
        data-testid="map-field"
        hide-details
        @update:model-value="(v) => ops.patchNode(node, { path: String(v ?? 'event') })"
      >
        <template #item="{ props: itemProps, item }">
          <v-list-item
            :title="(item as ListOption).title"
            :subtitle="(item as ListOption).subtitle"
            class="font-mono-title"
            v-bind="itemProps"
          />
        </template>
      </AutocompleteSearch>

      <div
        v-else-if="node.kind === 'constant'"
        class="d-flex ga-2"
      >
        <v-select
          :model-value="constType"
          :items="constTypeItems"
          :label="t('rules.map.const_type')"
          class="no-validation const-type flex-shrink-0"
          hide-details
          @update:model-value="setConstType"
        />
        <v-btn-toggle
          v-if="constType === 'bool'"
          :model-value="node.value"
          color="primary"
          class="flex-grow-1"
          mandatory
          divided
          @update:model-value="(v) => ops.patchNode(node, { value: Boolean(v) })"
        >
          <v-btn
            :value="true"
            class="flex-grow-1"
          >
            true
          </v-btn>
          <v-btn
            :value="false"
            class="flex-grow-1"
          >
            false
          </v-btn>
        </v-btn-toggle>
        <v-text-field
          v-else-if="constType !== 'null'"
          :model-value="node.value"
          :label="t('rules.map.value')"
          :type="constType === 'number' ? 'number' : 'text'"
          class="no-validation flex-grow-1"
          autocomplete="off"
          hide-details
          @update:model-value="setConst"
        />
      </div>

      <v-combobox
        v-else-if="node.kind === 'secret'"
        :model-value="node.name"
        :items="secretNames"
        :label="t('rules.map.secret_name')"
        :hint="t('rules.map.secret_hint')"
        :rules="secretRules"
        data-testid="map-secret"
        persistent-hint
        @update:model-value="(v) => ops.patchNode(node, { name: String(v ?? '').trim() })"
      >
        <template #prepend-inner>
          <span class="text-medium-emphasis font-mono text-body-2">env.</span>
        </template>
      </v-combobox>

      <AutocompleteSearch
        v-else-if="node.kind === 'helper'"
        :model-value="node.call"
        :items="helperItems"
        :label="t('rules.map.helper')"
        :search-fields="['subtitle']"
        class="no-validation"
        hide-details
        @update:model-value="(v) => ops.patchNode(node, { call: String(v ?? 'from') })"
      >
        <template #item="{ props: itemProps, item }">
          <v-list-item
            :title="(item as ListOption).title"
            :subtitle="(item as ListOption).subtitle"
            class="font-mono-title"
            v-bind="itemProps"
          />
        </template>
      </AutocompleteSearch>

      <v-textarea
        v-else-if="node.kind === 'expr'"
        :model-value="node.source"
        :label="t('rules.map.expression')"
        :hint="t('rules.map.expression_hint')"
        class="no-validation font-mono-input"
        rows="1"
        spellcheck="false"
        auto-grow
        persistent-hint
        @update:model-value="(v) => ops.patchNode(node, { source: String(v ?? '') })"
      />

      <div
        v-else
        class="text-body-2 text-medium-emphasis d-flex align-center h-100 py-3"
      >
        {{ node.kind === "object" ? t("rules.map.object_hint") : t("rules.map.list_hint") }}
      </div>
    </v-col>
  </v-row>
</template>

<style scoped lang="scss">
.const-type {
  flex: 0 0 128px;
  min-width: 0;
}

.font-mono-title :deep(.v-list-item-title),
.font-mono-input :deep(textarea) {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
}

.font-mono {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}
</style>
