import dns from "node:dns";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { spawn, type ChildProcess } from "node:child_process";
import type { FullConfig } from "@playwright/test";
import { loadStack, type StackInfo } from "./fixtures/stack";
import { signInOwner } from "./helpers/auth-api";

// uistack binds 127.0.0.1; prefer IPv4 so "localhost" in baseUrl does not hit ::1 on Windows.
dns.setDefaultResultOrder("ipv4first");

const here = dirname(fileURLToPath(import.meta.url));
const stackPath = join(here, ".stack.json");
const authDir = join(here, ".auth");
const authStatePath = join(authDir, "owner.json");

const readyzUrl = (baseUrl: string): string =>
  `${baseUrl.replace("://localhost", "://127.0.0.1")}/readyz`;

/**
 * Wait until `.stack.json` exists (and differs from `staleRaw` when a new stack
 * was spawned) and `/readyz` is OK. HTTP ready alone is not enough: the node
 * answers before seed rewrites credentials, which caused MFA invalids.
 */
const waitForStack = async (
  timeoutMs: number,
  opts: { staleRaw?: string | null } = {},
): Promise<void> => {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (existsSync(stackPath)) {
      try {
        const raw = readFileSync(stackPath, "utf8");
        if (opts.staleRaw != null && raw === opts.staleRaw) {
          await new Promise((r) => setTimeout(r, 1000));
          continue;
        }
        const info = JSON.parse(raw) as { baseUrl?: string };
        if (info.baseUrl) {
          const res = await fetch(readyzUrl(info.baseUrl));
          if (res.ok) return;
        }
      } catch {
        /* retry */
      }
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(
    `UI stack not ready after ${timeoutMs}ms (expected ${stackPath})`,
  );
};

const spawnUistack = (): ChildProcess => {
  const root = join(here, "..", "..");
  const child = spawn("go", ["run", "./test/uistack"], {
    cwd: root,
    env: {
      ...process.env,
      GOMAXPROCS: "2",
      GOFLAGS: "-p=2",
      EB_UISTACK_ADDR: process.env.EB_UISTACK_ADDR || "127.0.0.1:18765",
      PATH:
        process.platform === "win32"
          ? `C:\\Program Files\\Docker\\Docker\\resources\\bin;${process.env.PATH || ""}`
          : process.env.PATH,
    },
    stdio: ["ignore", "pipe", "pipe"],
    detached: process.platform !== "win32",
  });
  process.on("exit", () => {
    try {
      child.kill();
    } catch {
      /* ignore */
    }
  });
  child.stdout?.on("data", (buf: Buffer) => process.stdout.write(buf));
  child.stderr?.on("data", (buf: Buffer) => process.stderr.write(buf));
  return child;
};

const writeOwnerStorage = async (stack: StackInfo): Promise<void> => {
  const ctx = await signInOwner(stack);
  await ctx.patch("/v1/me/settings", {
    data: { ui: { theme: "light", locale: "en" } },
    headers: {
      Origin: stack.baseUrl,
      "X-Requested-With": "EmailBridge",
      "Content-Type": "application/json",
    },
  });
  mkdirSync(authDir, { recursive: true });
  const state = await ctx.storageState();
  writeFileSync(authStatePath, JSON.stringify(state, null, 2));
  await ctx.dispose();
};

export default async function globalSetup(_config: FullConfig): Promise<void> {
  if (process.env.EB_UI_BASE_URL) {
    const res = await fetch(readyzUrl(process.env.EB_UI_BASE_URL));
    if (!res.ok) throw new Error(`EB_UI_BASE_URL not ready: ${res.status}`);
  } else if (existsSync(stackPath)) {
    try {
      await waitForStack(5_000);
    } catch {
      const staleRaw = existsSync(stackPath)
        ? readFileSync(stackPath, "utf8")
        : null;
      spawnUistack();
      await waitForStack(300_000, { staleRaw });
    }
  } else {
    spawnUistack();
    await waitForStack(300_000, { staleRaw: null });
  }

  const stack = loadStack();
  await writeOwnerStorage(stack);
}

export { authStatePath };
