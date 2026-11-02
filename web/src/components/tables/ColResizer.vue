<script setup lang="ts">
// =========================================================================
// ColResizer — resize handle on the right edge of a header cell.
//
// Drag it (mouse, pen or touch), or focus it and press ←/→ (16 px; with Shift
// 64 px), to resize the column through useColumnResize: the columns to the
// right absorb the change, so the table stays exactly as wide as its box.
// Double-click resets the column to its default share.
//
// The drag captures the pointer (setPointerCapture), so it keeps tracking
// outside the handle and the window; moves are applied once per animation
// frame. While it runs the whole page shows the col-resize cursor and text
// cannot be selected.
//
// The header cell is sortable, so nothing the handle does may reach it:
// pointerdown/mousedown/click/dblclick/keydown stop here, and the click the
// browser may fire on the <th> after a drag is swallowed once (see
// swallowNextClick).
// =========================================================================

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import type { ColumnResize } from '@/composables/useColumnResize';

const props = defineProps<{
  resize: ColumnResize;
  /** Column key. */
  col: string;
  /** Column title, for the handle's accessible name. */
  label?: string;
}>();

const { t } = useI18n();

const STEP = 16;
const BIG_STEP = 64;
const BODY_CLASS = 'col-resizing';

// A focusable separator must report its value. Until the user resizes, the
// column has no stored width, so report the one it is rendered at (measured on
// mount and whenever the handle gets focus).
const handleEl = ref<HTMLElement | null>(null);
const measured = ref<number>();

const measure = (): void => {
  const th = handleEl.value?.closest('th');
  if (th) measured.value = Math.round(th.getBoundingClientRect().width);
};

onMounted(measure);

const range = computed(() => props.resize.range(props.col));

const cell = (): HTMLElement | null => handleEl.value?.closest('th') ?? null;

// After pointerup the browser may dispatch `click` to the nearest common
// ancestor of the down/up targets — the <th> itself when the drag ended inside
// the column — which would toggle the sort. Capture and drop that one click;
// if none comes, drop the listener on the next tick.
const swallowNextClick = (): void => {
  const swallow = (e: MouseEvent): void => {
    e.stopPropagation();
    e.preventDefault();
  };
  window.addEventListener('click', swallow, { capture: true, once: true });
  setTimeout(() => window.removeEventListener('click', swallow, { capture: true }), 0);
};

const raf = (fn: () => void): number =>
  typeof requestAnimationFrame === 'function' ? requestAnimationFrame(fn) : (setTimeout(fn, 16) as unknown as number);
const cancelRaf = (id: number): void =>
  typeof cancelAnimationFrame === 'function' ? cancelAnimationFrame(id) : clearTimeout(id);

interface DragSession {
  pointerId: number;
  startX: number;
  lastX: number;
  frame: number;
  /** The resize has begun: only on the first move, so a plain click never freezes the layout. */
  moved: boolean;
}

let session: DragSession | null = null;
const dragging = ref(false);

const apply = (): void => {
  if (!session?.moved) return;
  session.frame = 0;
  props.resize.drag(props.col, session.lastX - session.startX);
};

const finish = (save: boolean): void => {
  const s = session;
  if (!s) return;
  session = null;
  dragging.value = false;
  if (s.frame) cancelRaf(s.frame);
  if (save && s.moved) {
    props.resize.drag(props.col, s.lastX - s.startX);
    props.resize.commit();
    swallowNextClick();
  } else if (s.moved) {
    props.resize.cancel();
  }
  if (handleEl.value?.hasPointerCapture?.(s.pointerId)) handleEl.value.releasePointerCapture(s.pointerId);
  document.body.classList.remove(BODY_CLASS);
};

const onPointerdown = (e: PointerEvent): void => {
  if (e.button !== 0 || !cell() || session) return;
  e.preventDefault();
  handleEl.value?.setPointerCapture?.(e.pointerId);
  session = { pointerId: e.pointerId, startX: e.clientX, lastX: e.clientX, frame: 0, moved: false };
  dragging.value = true;
  document.body.classList.add(BODY_CLASS);
};

const onPointermove = (e: PointerEvent): void => {
  if (!session || e.pointerId !== session.pointerId) return;
  session.lastX = e.clientX;
  if (!session.moved) {
    const th = cell();
    if (e.clientX === session.startX || !th) return;
    if (!props.resize.begin(props.col, th)) {
      finish(false);
      return;
    }
    session.moved = true;
  }
  if (!session.frame) session.frame = raf(apply);
};

const onPointerup = (e: PointerEvent): void => {
  if (session && e.pointerId === session.pointerId) finish(true);
};

// The browser took the pointer over (e.g. a touch became a scroll): undo.
const onPointercancel = (e: PointerEvent): void => {
  if (session && e.pointerId === session.pointerId) finish(false);
};

// Capture lost without a pointerup (window blur, element removed): keep what was dragged.
const onLostCapture = (): void => finish(true);

// Keyboard: each step resizes at once; the store is written when the key is
// released (or focus leaves), so a held arrow key sends one PATCH, not dozens.
let keyPending = false;

const onKeydown = (e: KeyboardEvent): void => {
  const dir = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0;
  const th = cell();
  if (!dir || !th) return;
  e.preventDefault();
  props.resize.step(props.col, dir * (e.shiftKey ? BIG_STEP : STEP), th);
  keyPending = true;
};

const commitKeys = (): void => {
  if (!keyPending) return;
  keyPending = false;
  props.resize.commit();
};

const onKeyup = (e: KeyboardEvent): void => {
  if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') commitKeys();
};

const onDblclick = (): void => {
  const th = cell();
  if (th) props.resize.reset(props.col, th);
};

onBeforeUnmount(() => {
  finish(false);
  commitKeys();
});
</script>

<template>
  <span
    ref="handleEl"
    :aria-label="t('tables.resize_column', { column: label ?? col })"
    :aria-valuenow="range.now ?? measured"
    :aria-valuemin="range.min"
    :aria-valuemax="range.max"
    :data-resize-col="col"
    :class="{ 'col-resizer--active': dragging }"
    class="col-resizer"
    role="separator"
    aria-orientation="vertical"
    tabindex="0"
    @pointerdown.stop="onPointerdown"
    @pointermove="onPointermove"
    @pointerup="onPointerup"
    @pointercancel="onPointercancel"
    @lostpointercapture="onLostCapture"
    @mousedown.stop
    @click.stop
    @dblclick.stop.prevent="onDblclick"
    @focus="measure"
    @blur="commitKeys"
    @keydown.stop="onKeydown"
    @keyup.stop="onKeyup"
  />
</template>

<style lang="scss">
// Global (not scoped): it styles the <th> around the handle and the <body>
// during a drag too.

// A sticky header cell is already a containing block for the handle; only a
// non-sticky one needs `relative` (never override sticky with it).
.v-data-table__th:not(.v-data-table__th--sticky):has(.col-resizer) {
  position: relative;
}

.col-resizer {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: 10px;
  cursor: col-resize;
  touch-action: none;
  user-select: none;
  z-index: 1;

  &::after {
    content: '';
    position: absolute;
    top: 25%;
    bottom: 25%;
    right: 3px;
    width: 2px;
    border-radius: 1px;
    background: var(--border);
    opacity: 0;
    transition: opacity 0.15s;
  }

  &:focus-visible {
    outline: none;
  }
}

th:hover .col-resizer::after,
.col-resizer--active::after,
.col-resizer:focus-visible::after {
  opacity: 1;
  background: rgb(var(--v-theme-primary));
}

// While dragging: one cursor everywhere and no text selection.
body.col-resizing,
body.col-resizing * {
  cursor: col-resize !important;
  user-select: none !important;
}
</style>
