<script setup lang="ts">
/**
 * Accessible date-range filter. Desktop (≥ md) shows two adjacent months with one
 * shared header; phone shows one. Activator is content-sized and labelled.
 */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useDate, useDisplay, useLocale } from "vuetify";
import { mdiCalendarRange, mdiChevronLeft, mdiChevronRight, mdiClose } from "@mdi/js";
import {
  formatRangeLabel,
  monthParts,
  shiftMonth,
  startOfMonth,
} from "@/components/dateRangeField";
import { actionOf } from "@/constants/Actions";
import defaults from "@/core/config/VuetifyDefaults";

const filterAction = actionOf("filter");
const closeAction = actionOf("close");

const props = withDefaults(
  defineProps<{
    from: string;
    to: string;
    label: string;
    /** Longest selectable range in days, both ends included (0 = no limit). */
    maxDays?: number;
    /** Days after today cannot be picked. */
    noFuture?: boolean;
  }>(),
  { maxDays: 0, noFuture: false },
);

const emit = defineEmits<{
  "update:from": [value: string];
  "update:to": [value: string];
  change: [];
}>();

const { locale } = useI18n();
const { t: vt } = useLocale();
const { mdAndUp } = useDisplay();
const adapter = useDate();
const open = ref(false);
const viewAnchor = ref(startOfMonth(new Date()));

/** Nested defaults from VuetifyDefaults.DateRangeField — clear icon only. */
const clearDefaults = defaults.DateRangeField;

const toApiDate = (value: string): string => {
  if (!value) return "";
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};

const range = computed({
  get: (): Date[] => {
    if (!props.from && !props.to) return [];
    const start = props.from ? new Date(props.from) : null;
    if (!start || Number.isNaN(start.getTime())) return [];
    if (!props.to) return [start];
    const end = new Date(props.to);
    if (Number.isNaN(end.getTime())) return [start];
    return [start, end];
  },
  set: (value: unknown) => {
    const dates = Array.isArray(value) ? (value as Date[]) : [];
    if (!dates.length) {
      emit("update:from", "");
      emit("update:to", "");
      emit("change");
      return;
    }
    const start = new Date(dates[0]!);
    start.setHours(0, 0, 0, 0);
    emit("update:from", toApiDate(start.toISOString()));
    // Keep `to` empty until the second click so the picker stays in
    // "picking end" mode (model length 1) across both months.
    // Do not emit `change` yet — parents refetch/route-update on change
    // and would remount/close the menu mid-selection.
    if (dates.length < 2) {
      emit("update:to", "");
      return;
    }
    const end = new Date(dates[1]!);
    end.setHours(23, 59, 59, 999);
    emit("update:to", toApiDate(end.toISOString()));
    emit("change");
    open.value = false;
  },
});

const display = computed(() => {
  void locale.value;
  return formatRangeLabel(props.from, props.to);
});

const hasValue = computed(() => Boolean(display.value));

const left = computed(() => monthParts(viewAnchor.value));
const rightAnchor = computed(() => shiftMonth(viewAnchor.value, 1));
const right = computed(() => monthParts(rightAnchor.value));

const controlsText = computed(() => {
  const leftLabel = adapter.format(viewAnchor.value, "monthAndYear");
  if (!mdAndUp.value) return leftLabel;
  const rightLabel = adapter.format(rightAnchor.value, "monthAndYear");
  return `${leftLabel} – ${rightLabel}`;
});

const rangeStart = computed(() => range.value[0] ?? null);
const rangeEnd = computed(() => (range.value.length >= 2 ? range.value[1]! : null));

const dayBandClass = (date: unknown): string => {
  if (!rangeStart.value || !rangeEnd.value) return "";
  if (adapter.isSameDay(date, rangeStart.value) && adapter.isSameDay(date, rangeEnd.value)) {
    return "date-range-field__day--edge date-range-field__day--single";
  }
  if (adapter.isSameDay(date, rangeStart.value)) return "date-range-field__day--edge date-range-field__day--start";
  if (adapter.isSameDay(date, rangeEnd.value)) return "date-range-field__day--edge date-range-field__day--end";
  if (adapter.isWithinRange(date, [rangeStart.value, rangeEnd.value])) {
    return "date-range-field__day--middle";
  }
  return "";
};

const DAY_MS = 24 * 60 * 60 * 1000;
const dayNumber = (d: Date): number => Math.floor(new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() / DAY_MS);

/** Days the calendar offers: none after today with `noFuture`; while the end is being picked, only days
 * within `maxDays` of the start. */
const allowedDate = (value: unknown): boolean => {
  const d = new Date(value as string | number | Date);
  if (Number.isNaN(d.getTime())) return true;
  if (props.noFuture && dayNumber(d) > dayNumber(new Date())) return false;
  if (props.maxDays > 0 && rangeStart.value && !rangeEnd.value) {
    return Math.abs(dayNumber(d) - dayNumber(rangeStart.value)) < props.maxDays;
  }
  return true;
};

const syncViewToSelection = (): void => {
  const source = props.from
    ? new Date(props.from)
    : props.to
      ? new Date(props.to)
      : new Date();
  if (Number.isNaN(source.getTime())) {
    viewAnchor.value = startOfMonth(new Date());
    return;
  }
  viewAnchor.value = startOfMonth(source);
};

watch(open, (isOpen) => {
  if (isOpen) syncViewToSelection();
});

const onPrev = (): void => {
  viewAnchor.value = shiftMonth(viewAnchor.value, -1);
};

const onNext = (): void => {
  viewAnchor.value = shiftMonth(viewAnchor.value, 1);
};

const clear = (): void => {
  range.value = [];
};
</script>

<template>
  <div
    :class="{ 'date-range-field--clearable': hasValue }"
    class="date-range-field date-range-field--fit d-inline-flex align-center position-relative"
  >
    <v-menu
      v-model="open"
      :close-on-content-click="false"
      location="bottom start"
    >
      <template #activator="{ props: menuProps }">
        <!-- v-bind first on purpose: the explicit aria-* below must override the menu's own. -->
        <v-btn
          v-tippy="display || label"
          v-bind="menuProps"
          :prepend-icon="mdiCalendarRange"
          :color="filterAction.color"
          :aria-label="label"
          :aria-expanded="open"
          aria-haspopup="dialog"
          class="date-range-field__activator justify-start text-none"
        >
          <span class="d-block text-truncate">{{ display || label }}</span>
        </v-btn>
      </template>

      <div
        :aria-label="label"
        class="date-range-field__panel bg-surface"
        role="dialog"
      >
        <div class="date-range-field__controls d-flex align-center ga-1 px-3">
          <div class="date-range-field__controls-label flex-grow-1 text-body-1 font-weight-medium">
            {{ controlsText }}
          </div>
          <v-defaults-provider :defaults="clearDefaults">
            <v-btn
              :icon="mdiChevronLeft"
              :aria-label="vt('$vuetify.datePicker.ariaLabel.previousMonth')"
              data-testid="prev-month"
              @click="onPrev"
            />
            <v-btn
              :icon="mdiChevronRight"
              :aria-label="vt('$vuetify.datePicker.ariaLabel.nextMonth')"
              data-testid="next-month"
              @click="onNext"
            />
          </v-defaults-provider>
        </div>
        <div class="d-flex date-range-field__months">
          <v-date-picker-month
            v-model="range"
            :month="left.month"
            :year="left.year"
            :allowed-dates="allowedDate"
            multiple="range"
            color="primary"
            data-testid="date-range-month-left"
          >
            <template #day="{ props: dayProps, item }">
              <v-btn
                v-bind="dayProps"
                :class="dayBandClass(item.date)"
              >
                {{ item.localized }}
              </v-btn>
            </template>
          </v-date-picker-month>
          <v-date-picker-month
            v-if="mdAndUp"
            v-model="range"
            :month="right.month"
            :year="right.year"
            :allowed-dates="allowedDate"
            multiple="range"
            color="primary"
            data-testid="date-range-month-right"
          >
            <template #day="{ props: dayProps, item }">
              <v-btn
                v-bind="dayProps"
                :class="dayBandClass(item.date)"
              >
                {{ item.localized }}
              </v-btn>
            </template>
          </v-date-picker-month>
        </div>
      </div>
    </v-menu>

    <!-- Sibling of the activator (no button-in-button), laid over its end padding. -->
    <v-defaults-provider
      v-if="hasValue"
      :defaults="clearDefaults"
    >
      <v-btn
        :icon="mdiClose"
        :aria-label="vt('$vuetify.input.clear', label)"
        :color="closeAction.color"
        class="date-range-field__clear"
        data-testid="date-range-clear"
        @click="clear"
      />
    </v-defaults-provider>
  </div>
</template>

<style lang="scss" scoped>
.date-range-field--fit {
  width: fit-content;
  max-width: 100%;
}

// Match comfortable outlined fields (48 px), not the default large button (44 px).
// May shrink (label truncates) so the field never spills out of its column.
.date-range-field__activator {
  flex: 0 1 auto;
  width: auto;
  min-width: 0;
  max-width: 100%;
  height: 48px;
  min-height: 48px;

  // VBtn is an inline grid with an `auto` content track that will not go below its
  // text width; let it shrink so the label truncates instead of running under the ✕.
  grid-template-columns: max-content minmax(0, 1fr) max-content;

  :deep(.v-btn__content) {
    min-width: 0;
    overflow: hidden;
  }
}

// The clear ✕ sits inside the activator's end padding, so the pair is one
// 48 px control and never touches the next toolbar field.
$clear-size: 28px;
$clear-inset: 10px;

.date-range-field--clearable .date-range-field__activator {
  padding-inline-end: $clear-size + $clear-inset + 6px;
}

.date-range-field__clear {
  position: absolute;
  z-index: 1;
  top: 50%;
  inset-inline-end: $clear-inset;
  transform: translateY(-50%);
}

.date-range-field__panel {
  min-width: 0;
}

.date-range-field__controls {
  min-height: 48px;
}

.date-range-field__controls-label {
  color: rgb(var(--v-theme-on-surface));
  opacity: 1;
}

.date-range-field__months {
  min-width: 0;

  :deep(.v-date-picker-month__days) {
    column-gap: 0;
  }

  :deep(.v-date-picker-month__day) {
    width: 40px;
  }

  // Continuous in-range band behind the day buttons.
  :deep(.v-date-picker-month__day--selected) {
    &::before {
      content: "";
      position: absolute;
      z-index: 0;
      top: 50%;
      left: 0;
      right: 0;
      height: 32px;
      transform: translateY(-50%);
      background: rgb(var(--v-theme-primary), 0.14);
    }

    .v-btn {
      position: relative;
      z-index: 1;
    }
  }

  :deep(.date-range-field__day--middle) {
    border-radius: 0 !important;
    background: transparent !important;
    box-shadow: none !important;
    color: rgb(var(--v-theme-on-surface)) !important;
  }

  :deep(.date-range-field__day--start),
  :deep(.date-range-field__day--single) {
    border-radius: 12px 0 0 12px !important;
  }

  :deep(.date-range-field__day--end),
  :deep(.date-range-field__day--single) {
    border-radius: 0 12px 12px 0 !important;
  }

  :deep(.date-range-field__day--single) {
    border-radius: 12px !important;
  }

  :deep(.date-range-field__day--edge) {
    color: rgb(var(--v-theme-on-primary)) !important;
  }

  :deep(.v-date-picker-month__day--selected:has(.date-range-field__day--start)::before) {
    left: 4px;
    border-radius: 12px 0 0 12px;
  }

  :deep(.v-date-picker-month__day--selected:has(.date-range-field__day--end)::before) {
    right: 4px;
    border-radius: 0 12px 12px 0;
  }

  :deep(.v-date-picker-month__day--selected:has(.date-range-field__day--single)::before) {
    left: 4px;
    right: 4px;
    border-radius: 12px;
  }
}
</style>
