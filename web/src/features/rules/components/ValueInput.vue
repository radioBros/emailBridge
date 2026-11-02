<script setup lang="ts">
/** The value control that fits the field and op: chips, dropdowns, numbers, yes / no, time windows. */
import type { ListOption } from "@/features/rules/components/listItem";
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { RuleLeaf } from "@/api/rules";
import { EVENT_TYPES, eventGroupOf, eventTypeKey } from "@/features/rules/catalog/catalog";
import { opTakesList, type FieldDef } from "@/features/rules/model/builder";
import { enumLabel } from "@/features/rules/components/labels";
import AccountSelect from "@/features/rules/components/AccountSelect.vue";
import MailboxValue from "@/features/rules/components/MailboxValue.vue";
import TimezoneSelect from "@/features/rules/components/TimezoneSelect.vue";
import { useRuleContext } from "@/features/rules/composables/useRuleContext";

const props = defineProps<{
  leaf: RuleLeaf;
  def: FieldDef;
  /** Shown op (a folder row shows isInbox as eq / neq). */
  op: string;
  value: unknown;
}>();

const emit = defineEmits<{
  value: [value: unknown];
  name: [name: string];
}>();

const { t, te } = useI18n();
const ctx = useRuleContext();
const many = computed(() => opTakesList(props.op));

onMounted(() => {
  if (props.def.value === "list") void ctx.loadLists();
});

const set = (v: unknown): void => emit("value", v);

/** Single or multiple pick from a dropdown, depending on the op. */
const pick = (v: unknown): void => {
  if (many.value) list.value = Array.isArray(v) ? v : [];
  else set(v == null ? "" : v);
};

const text = computed({
  get: (): string => (props.value == null ? "" : String(props.value)),
  set: (v: string) => set(v),
});

const list = computed({
  get: (): string[] => (Array.isArray(props.value) ? props.value.map(String) : []),
  set: (v: unknown) =>
    set(
      (Array.isArray(v) ? v : [])
        .map((x) => String(x).trim())
        .filter(Boolean),
    ),
});

const number = computed({
  get: (): number | string => (typeof props.value === "number" ? props.value : ""),
  set: (v: number | string) => {
    const n = Number(v);
    set(v === "" || Number.isNaN(n) ? "" : n);
  },
});

const eventItems = computed(() =>
  EVENT_TYPES.map((e) => ({
    title: e,
    value: e,
    subtitle: te(eventTypeKey(e)) ? t(eventTypeKey(e)) : "",
    group: t(`rules.event_group.${eventGroupOf(e)}`),
  })),
);

const enumItems = computed(() =>
  (props.def.enumValues ?? []).map((v) => ({ title: enumLabel(t, te, props.def.id, v), value: v })),
);

const weekdayItems = computed(() =>
  [0, 1, 2, 3, 4, 5, 6].map((d) => ({ title: t(`rules.weekday.${d}`), value: d })),
);

const listItems = computed(() =>
  ctx.lists.value.map((l) => ({
    title: l.name,
    value: l.name,
    subtitle: t("rules.lists.row_summary", { kind: t(`rules.lists.kind_${l.kind}`), n: l.entryCount }),
  })),
);

const boolValue = computed({
  get: (): boolean => props.value !== false,
  set: (v: boolean) => set(v),
});

interface Hours {
  tz: string;
  window: string;
  days: string;
}

const hours = computed((): Hours => {
  const v = (props.value ?? {}) as Partial<Hours>;
  return { tz: v.tz ?? "UTC", window: v.window ?? "09:00-18:00", days: v.days ?? "Mon-Fri" };
});

const setHours = (patch: Partial<Hours>): void => set({ ...hours.value, ...patch });
const windowPart = (i: 0 | 1): string => hours.value.window.split("-")[i] ?? "";
const setWindow = (i: 0 | 1, v: string): void => {
  const parts = [windowPart(0), windowPart(1)];
  parts[i] = v;
  setHours({ window: parts.join("-") });
};

const dayItems = computed(() =>
  ["Mon-Fri", "Mon-Sat", "Sat-Sun", "Mon-Sun"].map((d) => ({ title: t(`rules.days.${d}`), value: d })),
);

const placeholder = computed((): string => {
  switch (props.def.id) {
    case "from.domain":
    case "to.domain":
      return "studio-legale.it";
    case "from.address":
      return "cancelleria@tribunale.it";
    case "attachment.name":
      return "*.pdf";
    case "attachment.type":
      return "application/pdf";
    case "header":
      return "list-id";
    default:
      return "";
  }
});
</script>

<template>
  <div class="d-flex flex-column ga-2 min-w-0">
    <v-btn-toggle
      v-if="def.value === 'bool'"
      v-model="boolValue"
      :aria-label="t('rules.builder.value')"
      color="primary"
      class="w-100"
      mandatory
      divided
    >
      <v-btn
        :value="true"
        class="flex-grow-1"
      >
        {{ t("rules.builder.yes") }}
      </v-btn>
      <v-btn
        :value="false"
        class="flex-grow-1"
      >
        {{ t("rules.builder.no") }}
      </v-btn>
    </v-btn-toggle>

    <AutocompleteSearch
      v-else-if="def.value === 'eventTypes'"
      :model-value="many ? list : text"
      :items="eventItems"
      :label="t('rules.builder.event_types')"
      :multiple="many"
      :chips="many"
      :closable-chips="many"
      :search-fields="['subtitle', 'group']"
      class="no-validation"
      data-testid="rule-event-types"
      hide-details
      @update:model-value="pick"
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

    <AccountSelect
      v-else-if="def.value === 'account'"
      :model-value="many ? list : text"
      :multiple="many"
      @update:model-value="(v) => set(v ?? (many ? [] : ''))"
    />

    <MailboxValue
      v-else-if="def.value === 'folder' || def.value === 'label'"
      :model-value="value"
      :kind="def.value"
      :multiple="many"
      :virtual-inbox="def.value === 'folder' && (op === 'eq' || op === 'neq')"
      @update:model-value="set"
    />

    <AutocompleteSearch
      v-else-if="def.value === 'enum' || def.value === 'specialUse'"
      :model-value="many ? list : text"
      :items="enumItems"
      :label="t('rules.builder.value')"
      :multiple="many"
      :chips="many"
      :closable-chips="many"
      class="no-validation"
      hide-details
      @update:model-value="pick"
    />

    <AutocompleteSearch
      v-else-if="def.value === 'list'"
      :model-value="leaf.name ?? ''"
      :items="listItems"
      :label="t('rules.builder.named_list')"
      :search-fields="['subtitle']"
      class="no-validation"
      data-testid="rule-list-select"
      hide-details
      @update:model-value="(v) => emit('name', String(v ?? ''))"
    >
      <template #item="{ props: itemProps, item }">
        <v-list-item
          :title="(item as ListOption).title"
          :subtitle="(item as ListOption).subtitle"
          v-bind="itemProps"
        />
      </template>
      <template #append>
        <v-list-item
          :title="t('rules.lists.manage')"
          to="/rule-lists"
          base-color="primary"
        />
      </template>
    </AutocompleteSearch>

    <AutocompleteSearch
      v-else-if="def.value === 'weekday'"
      :model-value="typeof value === 'number' ? value : null"
      :items="weekdayItems"
      :label="t('rules.builder.weekday')"
      class="no-validation"
      hide-details
      @update:model-value="(v) => set(Number(v))"
    />

    <template v-else-if="def.value === 'businessHours'">
      <TimezoneSelect
        :model-value="hours.tz"
        @update:model-value="(v) => setHours({ tz: v })"
      />
      <div class="d-flex ga-2">
        <v-text-field
          :model-value="windowPart(0)"
          :label="t('rules.builder.from_time')"
          class="no-validation"
          type="time"
          hide-details
          @update:model-value="(v) => setWindow(0, String(v))"
        />
        <v-text-field
          :model-value="windowPart(1)"
          :label="t('rules.builder.to_time')"
          class="no-validation"
          type="time"
          hide-details
          @update:model-value="(v) => setWindow(1, String(v))"
        />
      </div>
      <v-select
        :model-value="hours.days"
        :items="dayItems"
        :label="t('rules.builder.days')"
        class="no-validation"
        hide-details
        @update:model-value="(v) => setHours({ days: String(v) })"
      />
    </template>

    <v-text-field
      v-else-if="def.value === 'number'"
      v-model="number"
      :label="t('rules.builder.value')"
      class="no-validation"
      type="number"
      hide-details
    />

    <v-combobox
      v-else-if="many"
      v-model="list"
      :label="t('rules.builder.values')"
      :hint="t('rules.builder.values_hint')"
      class="no-validation"
      multiple
      chips
      closable-chips
    />

    <v-text-field
      v-else
      v-model="text"
      :label="t('rules.builder.value')"
      :placeholder="placeholder"
      class="no-validation"
      autocomplete="off"
      hide-details
    />
  </div>
</template>

<style scoped lang="scss">
.font-mono-title :deep(.v-list-item-title) {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
}
</style>
