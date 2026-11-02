import { defineConfig, devices } from "@playwright/test";


const readBaseURL = (): string =>
  process.env.EB_UI_BASE_URL || "http://localhost:18765";

export default defineConfig({
  testDir: "./e2e/specs",
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  // One worker: a shared storageState session cookie rotates / is invalidated when a
  // sibling re-auths, which cascaded into late dark-project login redirects (W46b).
  // Still under the machine resource cap (max 2).
  workers: 1,
  reporter: [
    ["list"],
    ["html", { open: "never", outputFolder: "playwright-report" }],
  ],
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: readBaseURL(),
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
    actionTimeout: 20_000,
  },
  globalSetup: "./e2e/global-setup.ts",
  projects: [
    {
      name: "phone-light",
      use: {
        browserName: "chromium",
        ...devices["Pixel 5"],
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        colorScheme: "light",
        userAgent:
          "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
      },
    },
    {
      name: "phone-dark",
      use: {
        browserName: "chromium",
        ...devices["Pixel 5"],
        viewport: { width: 390, height: 844 },
        isMobile: true,
        hasTouch: true,
        colorScheme: "dark",
        userAgent:
          "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
      },
    },
    {
      name: "desktop-light",
      use: {
        browserName: "chromium",
        viewport: { width: 1440, height: 900 },
        colorScheme: "light",
      },
    },
    {
      name: "desktop-dark",
      use: {
        browserName: "chromium",
        viewport: { width: 1440, height: 900 },
        colorScheme: "dark",
      },
    },
  ],
});
