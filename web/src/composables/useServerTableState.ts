import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { arrangeColumns, normalizeHeaders } from '@/helpers/TableColumns';
import type { ServerTableHeader } from '@/components/tables/types';

// =========================================================================
// State every table view (ServerTable, CardList) keeps the same way, so
// switching views never changes how search or columns behave.
// =========================================================================

/**
 * The search box: local, debounced, emitted as a string, and back to page 1.
 *
 * Seeded, not synced: the initial value does not fire the watcher, so restoring a search from the URL
 * does not also reset the page.
 */
export const useTableSearch = (
  initial: string,
  emit: { (e: 'update:page', value: number): void; (e: 'update:search', value: string): void },
) => {
  const searchLocal = ref(initial);
  let debounce: ReturnType<typeof setTimeout> | undefined;

  watch(searchLocal, (value) => {
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      emit('update:page', 1);
      // `clearable` emits null on clear: consumers always receive a string.
      emit('update:search', value ?? '');
    }, 320);
  });

  onBeforeUnmount(() => clearTimeout(debounce));

  return { searchLocal };
};

/**
 * Column visibility + order. ColManager restores the saved layout into these and owns the order (it
 * emits update:headers after a drag or a restore); the invariants live in helpers/TableColumns.
 */
export const useTableColumns = (headers: () => ServerTableHeader[]) => {
  const headersN = computed(() => normalizeHeaders(headers()));
  const forcedKeys = () => headersN.value.filter((h) => h.forced === true).map((h) => h.key);

  const visibleKeys = ref<string[]>(headersN.value.map((h) => h.key));
  const orderedHeaders = ref<ServerTableHeader[]>(headersN.value);

  /** The visible columns in the user's order (actions last, forced always shown). */
  const arranged = computed(() => arrangeColumns(headersN.value, orderedHeaders.value, visibleKeys.value));

  const onHeadersReordered = (next: ServerTableHeader[]) => {
    orderedHeaders.value = next;
  };

  // Headers can be DYNAMIC (columns per tab, a column hidden when empty). On a key-SET change: keep
  // surviving visible keys, reveal genuinely new ones (never re-reveal a user-hidden one), keep forced
  // ones. A pure label/locale change is a no-op.
  watch(
    () => headers().map((h) => h.key).join('|'),
    () => {
      const incoming = new Set(headersN.value.map((h) => h.key));
      const previous = new Set(orderedHeaders.value.map((h) => h.key));
      const surviving = visibleKeys.value.filter((k) => incoming.has(k));
      const added = [...incoming].filter((k) => !previous.has(k));
      visibleKeys.value = [...new Set([...surviving, ...added, ...forcedKeys()])];
      orderedHeaders.value = arrangeColumns(headersN.value, orderedHeaders.value, [...incoming]);
    },
  );

  return { headersN, visibleKeys, orderedHeaders, arranged, onHeadersReordered };
};
