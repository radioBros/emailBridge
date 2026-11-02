// =========================================================================
// UpdateCheck — is the server serving a newer build than this tab?
// =========================================================================
//
// The comparison is PURE and lives here so it can be unit-tested without a
// timer, a fetch or a Vue component. `components/UpdateMgr.vue` only does the
// plumbing around it.
//
// Requires the build-time half: `build/versionManifest.ts`.

/** Shape of the build-time `version.json` emitted by the versionManifest plugin. */
export interface VersionManifest {
  app_version?: string;
  git_sha?: string;
  built_at?: string;
}

/** What the RUNNING bundle was compiled with (`__APP_VERSION__` / `__GIT_SHA__`). */
export interface RunningBuild {
  version: string;
  sha: string;
}

export type UpdateReason = "sha" | "version" | null;

/**
 * Decides whether the tab is running an outdated build.
 *
 * SHA WINS when both sides have one: it changes on every commit, so it detects a
 * redeploy even when package.json was never bumped — which is the normal case
 * for most projects.
 *
 * Returns the REASON rather than a bare boolean so callers can log why, and so
 * tests can tell "matched on sha" from "matched on version".
 *
 * Deliberately CONSERVATIVE — anything ambiguous returns null (no prompt):
 *   * a manifest that failed to parse, or carries neither field;
 *   * a blank value on either side (an unset SHA must not read as a change, or
 *     every tab would prompt forever).
 *
 * That asymmetry is the point: a missed prompt costs a stale tab until the next
 * check, while a false prompt interrupts someone mid-task and teaches them to
 * dismiss it on sight.
 */
export const updateReason = (
  running: RunningBuild,
  manifest: VersionManifest | null,
): UpdateReason => {
  if (!manifest) return null;

  const serverSha = (manifest.git_sha ?? "").trim();
  const localSha = (running.sha ?? "").trim();
  if (serverSha && localSha) {
    return serverSha === localSha ? null : "sha";
  }

  const serverVersion = (manifest.app_version ?? "").trim();
  const localVersion = (running.version ?? "").trim();
  if (serverVersion && localVersion) {
    return serverVersion === localVersion ? null : "version";
  }

  return null;
};

/** Convenience boolean over {@link updateReason}. */
export const isOutdated = (running: RunningBuild, manifest: VersionManifest | null): boolean =>
  updateReason(running, manifest) !== null;

/**
 * Fetches the manifest, cache-busted.
 *
 * Returns null on ANY failure — offline, a 404, an HTML error page parsed as
 * JSON — because a failed check must never surface as "update available".
 *
 * Both cache defeats are load-bearing: `?ver=` beats an intermediate proxy that
 * ignores headers, and `cache: "no-store"` beats the browser's own cache. With
 * either missing the tab can be served the version.json it booted with, forever.
 */
export const fetchManifest = async (
  url = "/version.json",
  fetchImpl: typeof fetch = fetch,
): Promise<VersionManifest | null> => {
  try {
    const res = await fetchImpl(`${url}?ver=${Date.now()}`, { cache: "no-store" });
    if (!res.ok) return null;
    const data: unknown = await res.json();
    if (!data || typeof data !== "object") return null;
    return data as VersionManifest;
  } catch {
    return null;
  }
};
