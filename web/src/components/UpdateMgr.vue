<script setup lang="ts">
// =========================================================================
// UpdateMgr — prompts a reload when the server has a newer build.
// =========================================================================
//
// Mount it ONCE, high in the tree (App.vue), beside <router-view />. It renders
// NOTHING of its own: the prompt is the app's standard ConfirmDialog, so an
// update prompt looks like every other confirmation rather than a bespoke banner.
//
// How it decides: the build-time values compiled into THIS bundle
// (`__APP_VERSION__` / `__GIT_SHA__`) versus `version.json` fetched from the
// server. SHA wins when both sides have one. See `helpers/UpdateCheck.ts`.
//
// ⚠️ REQUIRES `build/versionManifest.ts` to be wired into vite.config.ts. Without
// the plugin there is no version.json to fetch (every check silently returns
// null); without the `define` block `__GIT_SHA__` is not even declared. Half-wired
// is the dangerous state — it compiles, runs, and can never fire.
//
// What happens AFTER the prompt is not this component's business: it hands the
// outcome to the shared `useUpdateAvailable` state, and a toolbar indicator reads
// it from there. One source of truth, so the dialog and the toolbar cannot
// disagree about whether this tab is stale.

import { onMounted, onUnmounted } from "vue";
import { mdiCloudDownload } from "@mdi/js";
import { useI18n } from "vue-i18n";
import { useConfirm } from "@/composables/useConfirm";
import { useUpdateAvailable } from "@/composables/useUpdateAvailable";
import { fetchManifest, updateReason, type UpdateReason } from "@/helpers/UpdateCheck";

const props = withDefaults(
  defineProps<{
    /** How often to re-check, ms. Default 5 minutes. */
    intervalMs?: number;
    /** Where the manifest lives. Must match the plugin's `fileName`. */
    manifestUrl?: string;
  }>(),
  { intervalMs: 5 * 60 * 1000, manifestUrl: "/version.json" },
);

const { t } = useI18n();
const confirm = useConfirm();

let timer: ReturnType<typeof setInterval> | null = null;

// `outdated` doubles as the ONE-PROMPT-PER-TAB LATCH: it never goes back to
// false, so the check stops asking once it has asked. Re-prompting someone who
// chose "Later" every 5 minutes is how a useful notice becomes something people
// reflexively dismiss — the toolbar indicator is what keeps the offer alive
// instead.
const { outdated, noteOutdated, dismissPrompt, reloadForUpdate } = useUpdateAvailable();

const check = async (): Promise<void> => {
  // A hidden tab is not being read; checking it wastes a request and queues a
  // dialog nobody is looking at. The next visible tick catches it.
  if (document.hidden || outdated.value) return;

  const manifest = await fetchManifest(props.manifestUrl);
  const reason: UpdateReason = updateReason(
    { version: __APP_VERSION__, sha: __GIT_SHA__ },
    manifest,
  );
  if (!reason) return;

  // Latch BEFORE awaiting the dialog: the await yields, and a visibilitychange
  // during it would otherwise start a second check and stack a second modal.
  noteOutdated(reason);

  const reload = await confirm({
    title: t("update.available_title"),
    body: t("update.available_body"),
    icon: mdiCloudDownload,
    variant: "info",
    confirmText: t("update.reload"),
    cancelText: t("update.later"),
  });

  if (reload) {
    // A FULL reload, not router navigation: the point is to drop the stale JS/CSS
    // this tab already holds in memory, which no in-app navigation can do.
    // Which reload, and why it takes no argument, is documented on
    // `reloadForUpdate()` — read it before adding a service worker.
    reloadForUpdate();
  } else {
    // "Later". The prompt is spent for this tab; the toolbar indicator takes over
    // so the user still has a way back to it.
    dismissPrompt();
  }
};

// Coming back to a tab left open overnight is the LIKELIEST moment to be stale,
// and waiting up to another full interval to say so is the wrong behaviour.
const onVisible = (): void => {
  if (!document.hidden) void check();
};

onMounted(() => {
  // NO CHECK ON MOUNT — the bundle that just loaded IS the server's current
  // build, so an immediate check can only produce a false positive when a deploy
  // lands mid-load. Start counting from now instead.
  timer = setInterval(() => {
    void check();
  }, props.intervalMs);
  document.addEventListener("visibilitychange", onVisible);
});

onUnmounted(() => {
  if (timer) clearInterval(timer);
  document.removeEventListener("visibilitychange", onVisible);
});

// Exposed for tests and for a manual "check now" if one is ever wanted.
defineExpose({ check });
</script>

<template>
  <!-- Renders nothing: the prompt is the shared ConfirmDialog. -->
</template>
