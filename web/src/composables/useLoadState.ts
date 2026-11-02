import { computed, ref } from "vue";

/**
 * The two kinds of "loading", kept mutually exclusive by construction.
 *
 * A screen that has never had data and a screen that is refreshing data it already shows are
 * different situations, and they need different affordances:
 *
 * | State | What the user sees | Why |
 * |---|---|---|
 * | **First load** | a **skeleton** shaped like the content | there is nothing to preserve, and a skeleton sets the layout so nothing jumps when data lands |
 * | **Refreshing** | a **spinner overlay** (`components/states/AppSectionLoader.vue`) over the existing content | the previous answer is still on screen and still readable; replacing it with a skeleton would throw away context and make the page flash |
 *
 * **Derived, never set by hand.** The obvious implementation is two booleans a caller maintains,
 * and the obvious bug is the two being true at once — a skeleton *and* an overlay, or neither.
 * Here `isFirstLoad` and `isRefreshing` are computed from one flag, so they cannot disagree and no
 * call site has to remember the invariant.
 *
 * **`hasLoadedOnce` flips on success only.** A retry after a failed first load is still a first
 * load: there is no content underneath, so an overlay would be floating over an error state. That
 * is why `finish` takes the outcome rather than living in a bare `finally`.
 */
export const useLoadState = () => {
  const isLoading = ref(false);
  /** True once a load has **succeeded**. A failed load leaves it alone. */
  const hasLoadedOnce = ref(false);

  /** Nothing on screen yet — render a shape-matched skeleton. */
  const isFirstLoad = computed(() => isLoading.value && !hasLoadedOnce.value);

  /** Content is on screen and being replaced — render an overlay over it. */
  const isRefreshing = computed(() => isLoading.value && hasLoadedOnce.value);

  const start = (): void => {
    isLoading.value = true;
  };

  /**
   * Ends a load.
   *
   * @param succeeded whether data actually arrived. Only a success promotes later loads to
   *   refreshes; a failure keeps the next attempt a first load.
   */
  const finish = (succeeded: boolean): void => {
    if (succeeded) hasLoadedOnce.value = true;
    isLoading.value = false;
  };

  /**
   * Forgets that anything was ever loaded.
   *
   * For a screen whose subject changes — a different employee, a different week — where the next
   * load genuinely has nothing to refresh and should show a skeleton again.
   */
  const reset = (): void => {
    hasLoadedOnce.value = false;
    isLoading.value = false;
  };

  return { isLoading, hasLoadedOnce, isFirstLoad, isRefreshing, start, finish, reset };
};
