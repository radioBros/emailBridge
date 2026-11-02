import { defineAsyncComponent, h, type AsyncComponentLoader, type Component } from "vue";
import { VSkeletonLoader } from "vuetify/components";

/**
 * A lazily-loaded component that shows a **skeleton of its own shape** while its chunk downloads,
 * instead of leaving a blank gap where it will appear.
 *
 * This is the *other* half of §10's loading story, and the one that is easy to miss. `useLoadState`
 * covers waiting for **data**; this covers waiting for **code**. A panel whose data has already
 * arrived still renders nothing until its JavaScript chunk lands, so a screen that lazy-loads its
 * sections flashes empty space between the data arriving and the section appearing — even though
 * nothing is "loading" by any flag the page holds.
 *
 * ```ts
 * const ProfileEditor = asyncWithSkeleton(
 *   () => import("@/components/ProfileEditor.vue"),
 *   "list-item-two-line@3, table-tbody",
 * );
 * ```
 *
 * `type` is any `v-skeleton-loader` type string. Comma-separate to stack shapes and use `@n` to
 * repeat one: `"text, button@4"` is a line of text above four buttons. Shape it like the component
 * it stands in for, for the same reason a data skeleton mirrors its content — a placeholder of the
 * wrong shape makes the page lay out twice.
 *
 * **Two things that will bite you:**
 *
 * 1. **`delay: 0` is deliberate.** Vue waits 200ms before showing `loadingComponent`, on the theory
 *    that a fast load should not flash a placeholder. For a data request that is right; for a chunk
 *    that usually resolves in well under 200ms it means the placeholder never appears at all and
 *    you keep the blank gap this exists to remove.
 * 2. **`loadingComponent` is mounted as a component and receives no props.** It cannot be a vnode,
 *    which is why the skeleton is wrapped in an arrow function rather than passed as `h(...)`
 *    directly — the wrapper is the component, and it closes over `type`.
 *
 * Note this import of `VSkeletonLoader` is **static**, for the same reason §10 insists skeletons are
 * imported statically: a placeholder fetched over the network cannot cover the wait it exists for.
 *
 * @param loader - the `() => import(...)` for the real component
 * @param type - skeleton shape shown while it loads
 */
export const asyncWithSkeleton = (
  loader: AsyncComponentLoader,
  type: string = "paragraph",
): Component =>
  defineAsyncComponent({
    loader,
    loadingComponent: () => h(VSkeletonLoader, { type }),
    delay: 0,
  });
