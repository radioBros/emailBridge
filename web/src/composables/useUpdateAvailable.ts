// =========================================================================
// useUpdateAvailable — "is this tab running an older build?", app-wide.
// =========================================================================
//
// `UpdateMgr.vue` (mounted once, at the app root) owns the polling and the
// prompt. This owns the ANSWER, so a second consumer — a toolbar indicator in
// your app bar — can read it WITHOUT polling a second time and without being
// able to disagree with the prompt.
//
// State is MODULE-LEVEL on purpose. A per-caller `ref()` inside the composable
// would let the toolbar say "up to date" while the dialog on top of it says
// otherwise. There is one build running in one tab, so there is one answer.
//
// TWO flags, not one, and the distinction is the whole feature:
//   * `outdated`        — a newer build was detected. Also the one-prompt-per-tab
//                         latch: once true, `check()` stops asking.
//   * `promptDismissed` — the user chose "Later".
//
// The indicator appears only when BOTH are true. On detection alone it would be
// drawn behind the open modal; without a prompt having happened it would be a
// notice nobody was ever told about.
//
// NOTHING HERE IS PERSISTED, and that is deliberate. A flag written to
// localStorage would survive the reload that fixes it, so the freshly-loaded
// tab would show a stale "new version available" indicator it has no way to
// clear — an inescapable loop. In memory means it dies with the tab, which is
// correct: a tab that reloads IS the new build.

import { computed, ref, type ComputedRef, type Ref } from "vue";
import type { UpdateReason } from "@/helpers/UpdateCheck";

// Shared across every caller — see the header.
const outdated = ref(false);
const promptDismissed = ref(false);
const reason = ref<UpdateReason>(null);

export interface UpdateAvailable {
  /** A newer build is on the server. Latches: never goes back to false. */
  outdated: Ref<boolean>;
  /** The user saw the prompt and chose "Later". */
  promptDismissed: Ref<boolean>;
  /** Why we think so ('sha' | 'version'), for logging and tests. */
  reason: Ref<UpdateReason>;
  /** The toolbar indicator's visibility: outdated AND already prompted. */
  showIndicator: ComputedRef<boolean>;
  /** Called by UpdateMgr the moment a newer build is detected. */
  noteOutdated: (why: UpdateReason) => void;
  /** Called by UpdateMgr when the prompt is cancelled. */
  dismissPrompt: () => void;
  /** Load the new build. See the comment on the implementation. */
  reloadForUpdate: () => void;
}

const noteOutdated = (why: UpdateReason): void => {
  outdated.value = true;
  reason.value = why;
};

const dismissPrompt = (): void => {
  promptDismissed.value = true;
};

// WHICH RELOAD, AND WHY.
//
// A plain, argument-less `location.reload()`.
//
// NOT `location.reload(true)`: the boolean was removed from the HTML spec years
// ago and is silently ignored by every current browser, so writing it LOOKS like
// a force-refresh while doing nothing at all.
//
// A plain reload suffices in a stock Vite app because:
//   * Vite content-hashes every JS/CSS asset, so the new build's assets have
//     names this tab has never requested and cannot have cached;
//   * the only cacheable thing left is index.html, and the cache-busted
//     (`?ver=` + `cache: "no-store"`) version.json fetch that got us here has
//     already demonstrated the server is serving something newer.
//
// ⚠️ THIS IS NOT ENOUGH IF YOU ADD A SERVICE WORKER — and README §9 tells you to
// generate PWA assets, so adding `vite-plugin-pwa` is the LIKELY case, not an
// exotic one. A precaching worker serves the OLD bundle from its own cache on
// reload, the version check fires again immediately, and the indicator comes
// straight back — a loop the user cannot escape by clicking the only control
// offered.
//
// With a worker, tell the WAITING worker to take over BEFORE reloading:
//
//   const reg = await navigator.serviceWorker.getRegistration();
//   reg?.waiting?.postMessage({ type: "SKIP_WAITING" });
//   // then reload on the controllerchange event, not immediately
//
// `vite-plugin-pwa`'s `useRegisterSW().updateServiceWorker(true)` does both, and
// is what `reloadForUpdate` should delegate to in a PWA build.
const reloadForUpdate = (): void => {
  window.location.reload();
};

export const useUpdateAvailable = (): UpdateAvailable => ({
  outdated,
  promptDismissed,
  reason,
  showIndicator: computed(() => outdated.value && promptDismissed.value),
  noteOutdated,
  dismissPrompt,
  reloadForUpdate,
});
