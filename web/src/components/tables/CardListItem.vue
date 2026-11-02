<script setup lang="ts" generic="T extends Record<string, unknown>">
// =========================================================================
// CardListItem — the default card of CardList: a reference to copy, not a
// component to configure. The first visible column is the title, the others
// are label/value rows, and the actions column sits beside the title.
//
// Every value renders through the consumer's `#item.<key>` slot when there is
// one (the same slot the table cell uses), so a column looks the same in both
// views.
// =========================================================================

import { computed } from 'vue';
import { isActionKey } from '@/helpers/TableColumns';
import type { ServerTableCellProps, ServerTableHeader } from './types';

const props = defineProps<{
  item: T;
  index: number;
  /** Visible columns, in the user's order. */
  columns: ServerTableHeader[];
  /** Pointer, hover, keyboard focus (Enter/Space), and `select` is emitted on activation. */
  clickable?: boolean;
}>();

const emit = defineEmits<{
  select: [event: Event];
}>();

const slots = defineSlots<{
  [cell: `item.${string}`]: (props: ServerTableCellProps<T>) => unknown;
}>();

const content = computed(() => props.columns.filter((c) => !isActionKey(c.key)));
const titleColumn = computed(() => content.value[0]);
const fields = computed(() => content.value.slice(1));
// The actions column has no value of its own: it shows only when the consumer renders it.
const actionColumns = computed(() => props.columns.filter((c) => isActionKey(c.key) && slots[`item.${c.key}`]));

const cell = (col: ServerTableHeader): ServerTableCellProps<T> => ({
  item: props.item,
  index: props.index,
  value: props.item[col.key],
});

const display = (col: ServerTableHeader): string => {
  const value = props.item[col.key];
  return value === null || value === undefined ? '' : String(value);
};

const onClick = (event: Event): void => emit('select', event);

// VCard gives an onClick-only card no keyboard access. Enter/Space on the CARD
// itself (not on an action button inside it) activate it, like a button.
const onKeydown = (event: KeyboardEvent): void => {
  if (event.target !== event.currentTarget || (event.key !== 'Enter' && event.key !== ' ')) return;
  event.preventDefault();
  emit('select', event);
};
</script>

<template>
  <v-card
    :link="clickable"
    :tabindex="clickable ? 0 : undefined"
    class="d-flex flex-column h-100"
    v-on="clickable ? { click: onClick, keydown: onKeydown } : {}"
  >
    <v-card-item
      v-if="titleColumn || actionColumns.length"
      class="pb-2"
    >
      <v-card-title
        v-if="titleColumn"
        class="text-title-medium font-weight-bold"
      >
        <slot
          :name="`item.${titleColumn.key}`"
          v-bind="cell(titleColumn)"
        >
          {{ display(titleColumn) }}
        </slot>
      </v-card-title>

      <!-- Actions sit by the title, not in a footer bar of their own. An action
           is not a click on the record, so it must not also fire click:row. -->
      <template
        v-if="actionColumns.length"
        #append
      >
        <div
          class="d-flex align-center ga-1"
          @click.stop
        >
          <template
            v-for="col in actionColumns"
            :key="col.key"
          >
            <slot
              :name="`item.${col.key}`"
              v-bind="cell(col)"
            />
          </template>
        </div>
      </template>
    </v-card-item>

    <v-card-text
      v-if="fields.length"
      class="flex-grow-1 pt-0"
    >
      <dl class="d-flex flex-column ga-2 ma-0">
        <div
          v-for="col in fields"
          :key="col.key"
          class="d-flex align-center justify-space-between ga-4"
        >
          <dt class="text-body-small text-medium-emphasis text-no-wrap">
            {{ col.title }}
          </dt>
          <!-- Long unbroken values (IDs, addresses) wrap inside the card instead of running past
               its edge: the value may shrink (min-w-0) and break anywhere (text-break). -->
          <dd class="text-body-medium text-end text-break min-w-0 ma-0">
            <slot
              :name="`item.${col.key}`"
              v-bind="cell(col)"
            >
              {{ display(col) }}
            </slot>
          </dd>
        </div>
      </dl>
    </v-card-text>
  </v-card>
</template>
