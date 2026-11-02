import { computed, onBeforeUnmount, onMounted, ref, watch, type ComputedRef, type Ref } from "vue";
import { useLayout } from "vuetify";
import { TABLE_FILL_MIN_HEIGHT } from "@/core/config/UiLayout";
import { fillHeight, screenBoxHeight, spaceBelow, viewportHeight } from "@/helpers/FillHeight";

type ElementGetter = () => HTMLElement | null | undefined;

// Writes happen in a frame, never in the ResizeObserver callback itself: a size written there
// re-triggers observers in the same pass ("ResizeObserver loop completed…").
const useFrame = (run: () => void): { schedule: () => void; cancel: () => void } => {
  let frame = 0;
  const schedule = (): void => {
    if (frame) return;
    if (typeof requestAnimationFrame !== "function") {
      run();
      return;
    }
    frame = requestAnimationFrame(() => {
      frame = 0;
      run();
    });
  };
  const cancel = (): void => {
    if (frame && typeof cancelAnimationFrame === "function") cancelAnimationFrame(frame);
    frame = 0;
  };
  return { schedule, cancel };
};

const isHidden = (el: HTMLElement): boolean => el.getClientRects().length === 0;

/**
 * Measured fill height: the px height that makes `target` end exactly at the viewport bottom,
 * minus the page's bottom padding (helpers/FillHeight). `null` while disabled or not yet
 * measured — the consumer then keeps its CSS fallback.
 *
 * Re-measured (once per frame) when the box, anything beside it on the way up to the main area,
 * the main area or the window resize, or when elements come or go along that path: a drawer
 * collapsing, filter rows wrapping, tabs, a banner or a bulk bar appearing (or going). A hidden box (inactive tab, `v-show`) keeps its last value and is
 * measured again once it shows.
 */
export const useFillHeight = (
  target: ElementGetter,
  options: { enabled?: () => boolean; min?: number } = {},
): Ref<number | null> => {
  const height = ref<number | null>(null);
  const enabled = options.enabled ?? (() => true);
  const min = options.min ?? TABLE_FILL_MIN_HEIGHT;

  const measure = (): void => {
    const el = target();
    if (!el || !enabled()) {
      height.value = null;
      return;
    }
    if (isHidden(el)) return;
    const next = fillHeight({
      viewport: viewportHeight(),
      top: el.getBoundingClientRect().top + window.scrollY,
      below: spaceBelow(el),
      min,
    });
    if (next !== height.value) height.value = next;
  };

  const { schedule, cancel } = useFrame(measure);
  let observer: ResizeObserver | null = null;
  let mutations: MutationObserver | null = null;

  // What can move or resize the box: the box itself, every element beside it on the way up to the
  // main area (filter rows, banners, a bulk bar, the side column that sets a row's height) and
  // the main area. Elements coming and going at any of those levels (`v-if`) are caught by a
  // childList observer on each ancestor, which also re-collects the set. A box only MOVING never
  // resizes it, and a stretched column or the viewport-clamped main does not resize when content
  // above shrinks — so the elements themselves are watched, not just their containers.
  const observe = (): void => {
    observer?.disconnect();
    mutations?.disconnect();
    const el = target();
    if (!el) return;
    observer?.observe(el);
    const main = el.closest(".v-main");
    for (let node: HTMLElement = el; node.parentElement && node !== main; node = node.parentElement) {
      const parent = node.parentElement;
      mutations?.observe(parent, { childList: true });
      for (let sib = parent.firstElementChild; sib; sib = sib.nextElementSibling) {
        if (sib !== node) observer?.observe(sib);
      }
    }
    if (main) observer?.observe(main);
  };

  const onMutation = (): void => {
    observe();
    schedule();
  };

  onMounted(() => {
    if (typeof ResizeObserver !== "undefined") observer = new ResizeObserver(schedule);
    if (typeof MutationObserver !== "undefined") mutations = new MutationObserver(onMutation);
    observe();
    window.addEventListener("resize", schedule, { passive: true });
    schedule();
  });

  watch([target, enabled], () => {
    observe();
    schedule();
  }, { flush: "post" });

  onBeforeUnmount(() => {
    observer?.disconnect();
    mutations?.disconnect();
    window.removeEventListener("resize", schedule);
    cancel();
  });

  return height;
};

/**
 * The inner height of a flex-sized box (`clientHeight`), kept current with a ResizeObserver —
 * for a child that needs the box's height as a number (a virtual table's `height`, an infinite
 * list's `max-height`). `null` until measured, or while the box is missing or hidden.
 */
export const useBoxHeight = (target: ElementGetter): Ref<number | null> => {
  const height = ref<number | null>(null);

  const measure = (): void => {
    const el = target();
    if (!el || isHidden(el)) return;
    const next = el.clientHeight;
    if (next > 0 && next !== height.value) height.value = next;
  };

  const { schedule, cancel } = useFrame(measure);
  let observer: ResizeObserver | null = null;

  const observe = (): void => {
    observer?.disconnect();
    const el = target();
    if (observer && el) observer.observe(el);
  };

  onMounted(() => {
    if (typeof ResizeObserver !== "undefined") observer = new ResizeObserver(schedule);
    observe();
    schedule();
  });

  watch(target, () => {
    observe();
    schedule();
  }, { flush: "post" });

  onBeforeUnmount(() => {
    observer?.disconnect();
    cancel();
  });

  return height;
};

/**
 * The CSS height of a list box as tall as the screen below the app bar (helpers/FillHeight
 * `screenBoxHeight`). The bar heights are measured by Vuetify's layout (`mainRect`: what the app
 * bar and a bottom navigation reserve), so they follow the bar instead of a hard-coded number.
 * Outside a Vuetify layout (a test mount) the bars count as 0.
 */
export const useScreenBoxHeight = (min = TABLE_FILL_MIN_HEIGHT): ComputedRef<string> => {
  let layout: ReturnType<typeof useLayout> | null = null;
  try {
    layout = useLayout();
  } catch {
    layout = null;
  }
  return computed(() =>
    screenBoxHeight({ top: layout?.mainRect.value.top ?? 0, bottom: layout?.mainRect.value.bottom ?? 0, min }),
  );
};
