// =========================================================================
// versionManifest — the BUILD-TIME half of the update check.
// =========================================================================
//
// This is a **Vite** plugin, which is why it lives in `build/` and not in
// `plugins/`: `plugins/vuetify.ts` is a *Vue* plugin imported from `src` at
// runtime, whereas this one is imported by `vite.config.ts` and runs in Node at
// build time. Putting them in the same folder reads as one concept and they are
// two.
//
// ⚠️ THE COMPOSABLE CANNOT WORK WITHOUT THIS FILE. `useUpdateAvailable` and
// `UpdateMgr.vue` only compare two values; this is what produces them. Copy the
// component without wiring the plugin and the check is inert by construction —
// see the `resolveGitSha` note below for what that failure actually looked like
// in production.
//
// Wire BOTH halves in `vite.config.ts`:
//
//   import { versionManifest, resolveGitSha, readPkgVersion } from "./build/versionManifest";
//
//   const pkgVersion = readPkgVersion(import.meta.url);
//   const gitSha = resolveGitSha();
//
//   export default defineConfig({
//     define: {
//       __APP_VERSION__: JSON.stringify(pkgVersion),
//       __GIT_SHA__: JSON.stringify(gitSha),
//     },
//     plugins: [versionManifest({ appVersion: pkgVersion, gitSha }), vue(), vuetify()],
//   });
//
// `define` compiles the values into the RUNNING bundle; the plugin writes the
// same values to `version.json` in the build output. `UpdateMgr` fetches the
// file and compares it against its own compiled-in constants, so a difference
// means the server has a newer build than this open tab.

import type { Plugin } from "vite";
import { readFileSync } from "node:fs";
import { execSync } from "node:child_process";
import { fileURLToPath, URL } from "node:url";

/** Reads `version` out of the project's package.json at build time. */
export const readPkgVersion = (importMetaUrl: string): string =>
  (
    JSON.parse(
      readFileSync(fileURLToPath(new URL("./package.json", importMetaUrl)), "utf-8"),
    ) as { version?: string }
  ).version ?? "";

/**
 * The commit the bundle was built from — the only value that changes on **every**
 * deploy, and therefore the only one the check can rely on in a project that does
 * not bump package.json per release.
 *
 * ⚠️ DERIVE IT, DO NOT ASK SOMEONE TO REMEMBER IT. The reference implementation
 * originally read `process.env.VITE_GIT_SHA ?? ""`, i.e. empty unless someone
 * prefixed the build command. Nobody ever did: production served
 * `git_sha: ""` against a pinned `app_version`, so the comparison matched two
 * identical versions and **the prompt could never fire**. Every tab left open
 * across a deploy stayed silently stale for months.
 *
 * Order: env var wins (CI can set it with no git history), then `git rev-parse`,
 * then empty. Empty is still SAFE — `updateReason` treats a blank SHA as "no
 * information" rather than as a change, and falls back to the version compare.
 */
export const resolveGitSha = (): string => {
  const fromEnv = (process.env["VITE_GIT_SHA"] ?? "").trim();
  if (fromEnv) return fromEnv;
  try {
    return execSync("git rev-parse --short HEAD", { stdio: ["ignore", "pipe", "ignore"] })
      .toString()
      .trim();
  } catch {
    // No git: a tarball build, or a CI image without the history. The check
    // degrades to comparing app_version, which is the pre-existing behaviour.
    return "";
  }
};

export interface VersionManifestOptions {
  /** Usually `readPkgVersion(import.meta.url)`. */
  appVersion: string;
  /** Usually `resolveGitSha()`. */
  gitSha: string;
  /** Output name. Must match `UpdateMgr`'s `manifestUrl` prop. */
  fileName?: string;
}

/**
 * Emits `version.json` into the build output.
 *
 * It must be an EMITTED ASSET rather than a file in `public/`, because the values
 * are only known at build time — a checked-in `public/version.json` would have to
 * be edited by hand on every deploy, which is the same "someone has to remember"
 * failure as the env var above.
 */
export const versionManifest = (options: VersionManifestOptions): Plugin => ({
  name: "version-manifest",
  generateBundle() {
    this.emitFile({
      type: "asset",
      fileName: options.fileName ?? "version.json",
      source: JSON.stringify(
        {
          app_version: options.appVersion,
          git_sha: options.gitSha,
          built_at: new Date().toISOString(),
        },
        null,
        2,
      ),
    });
  },
});
