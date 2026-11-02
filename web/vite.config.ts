import { fileURLToPath, URL } from "node:url";
import { defineConfig } from "vitest/config";
import vue from "@vitejs/plugin-vue";
import vuetify from "vite-plugin-vuetify";
import { versionManifest, resolveGitSha, readPkgVersion } from "./build/versionManifest";

const pkgVersion = readPkgVersion(import.meta.url);
const gitSha = resolveGitSha();

export default defineConfig({
  define: {
    __APP_VERSION__: JSON.stringify(pkgVersion),
    __GIT_SHA__: JSON.stringify(gitSha),
  },
  plugins: [
    versionManifest({ appVersion: pkgVersion, gitSha }),
    vue(),
    vuetify({
      autoImport: true,
      styles: { configFile: "src/styles/settings.scss" },
    }),
  ],
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    // Fixed: the dev stack (deploy/compose/devdeps.yml) allows only this origin.
    port: 5173,
    strictPort: true,
    proxy: {
      "/v1": {
        target: "http://127.0.0.1:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
  test: {
    environment: "happy-dom",
    globals: true,
    setupFiles: ["./tests/setup.ts"],
    css: true,
    // Playwright owns e2e/specs — vitest must not load those files.
    exclude: ["**/node_modules/**", "**/dist/**", "**/e2e/**"],
    // Guard against open handles (e.g. unmocked settings.load → :3000) hanging the suite forever.
    testTimeout: 30_000,
    hookTimeout: 15_000,
    teardownTimeout: 15_000,
    server: {
      deps: {
        inline: ["vuetify", "vue-tippy"],
      },
    },
    coverage: {
      provider: "v8",
      reporter: ["text", "html"],
      // Logic modules for the shell; Vue SFCs are covered by Playwright in later phases.
      include: [
        "src/api/client.ts",
        "src/api/problems.ts",
        "src/composables/useLoadState.ts",
        "src/composables/useRealtime.ts",
        "src/composables/useProblem.ts",
        "src/constants/Actions.ts",
        "src/constants/Statuses.ts",
        "src/core/config/**/*.ts",
        "src/stores/notify.ts",
        "src/stores/settings.ts",
        "src/helpers/CardList.ts",
        "src/helpers/TableColumns.ts",
        "src/helpers/UpdateCheck.ts",
        "src/features/home/composables/**/*.ts",
        "src/features/auth/composables/**/*.ts",
        "src/api/routes.ts",
        "src/features/routes/composables/**/*.ts",
        "src/features/routes/celHighlight.ts",
      ],
      exclude: ["src/**/*.d.ts"],
      thresholds: {
        lines: 80,
      },
    },
  },
});

