import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("@/api/settings", () => ({
  getSettings: vi.fn(async () => ({
    body: {
      version: 3,
      values: {
        webhookTimeout: { value: "30s", source: "default", scope: "organization" },
        authMfaRequired: { value: true, source: "env", scope: "organization" },
      },
      switches: {
        "sandbox.enabled": { value: false, source: "default", scope: "organization" },
      },
    },
    etag: "v3",
  })),
  patchSettings: vi.fn(async (_scope: string, patch: Record<string, unknown>) => ({
    body: {
      version: 4,
      values: {
        webhookTimeout: {
          value: patch.webhookTimeout ?? "30s",
          source: "organization",
          scope: "organization",
        },
        authMfaRequired: { value: true, source: "env", scope: "organization" },
      },
      switches: {},
    },
    etag: "v4",
  })),
  listFeatureSwitches: vi.fn(async () => ({
    data: [
      {
        name: "sandbox.enabled",
        description: "Sandbox",
        scopes: ["global", "organization"],
        default: false,
        phase: "P1",
        effective: false,
        source: "default",
      },
    ],
  })),
}));

vi.mock("@/api/auth", () => ({
  auth: {
    useSession: () => ({ value: { data: null, isPending: false } }),
    signout: vi.fn(),
  },
}));

vi.mock("@/api/me", () => ({
  getMe: vi.fn(async () => ({
    kind: "user",
    mfa: true,
    permissions: [],
    memberships: [],
    role: "owner",
    user: {
      id: "u1",
      email: "a@b.c",
      name: "Ada",
      locale: "en",
      instanceRole: "owner",
      status: "active",
    },
  })),
  patchMe: vi.fn(),
  getMySettings: vi.fn(),
  patchUiSettings: vi.fn(),
}));

vi.mock("@/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
  return {
    ...actual,
    configureApiClient: vi.fn(),
  };
});

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({ messageOf: (e: Error) => e.message, fieldErrors: () => ({}) }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k, te: () => false, locale: { value: "en" } }),
}));

describe("useSettings", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("loads settings, tracks dirty keys and saves only changes", async () => {
    const { useSessionStore } = await import("@/stores/session");
    const session = useSessionStore();
    session.me = {
      kind: "user",
      mfa: true,
      permissions: [],
      memberships: [],
      role: "owner",
      user: {
        id: "u1",
        email: "a@b.c",
        name: "Ada",
        locale: "en",
        instanceRole: "owner",
        status: "active",
      },
    };

    const { useSettings } = await import("@/features/settings/composables/useSettings");
    const { patchSettings } = await import("@/api/settings");
    const s = useSettings();
    await s.load();

    expect(s.body.value?.version).toBe(3);
    expect(s.isDirty.value).toBe(false);

    s.setDraft("webhookTimeout", "45s");
    expect(s.isDirty.value).toBe(true);
    expect(s.dirtyKeys.value).toEqual(["webhookTimeout"]);

    s.tab.value = "webhooks";
    await s.saveTab();
    expect(patchSettings).toHaveBeenCalledWith(
      "organization",
      { webhookTimeout: "45s" },
      "v3",
    );
    expect(s.isDirty.value).toBe(false);
    expect(s.etag.value).toBe("v4");
  });

  it("locks global-only keys at organisation scope", async () => {
    const { useSessionStore } = await import("@/stores/session");
    const session = useSessionStore();
    session.me = {
      kind: "user",
      mfa: true,
      permissions: [],
      memberships: [],
      role: "admin",
      user: {
        id: "u1",
        email: "a@b.c",
        name: "Ada",
        locale: "en",
        instanceRole: "",
        status: "active",
      },
    };

    const { useSettings } = await import("@/features/settings/composables/useSettings");
    const s = useSettings();
    await s.load();
    s.scope.value = "organization";
    expect(
      s.isLocked("protocolLogs.rawDebugTtl", {
        key: "protocolLogs.rawDebugTtl",
        kind: "duration",
        changeScope: "global",
      }),
    ).toBe(true);
  });

  it("tracks switch drafts and discard", async () => {
    const { useSessionStore } = await import("@/stores/session");
    const session = useSessionStore();
    session.me = {
      kind: "user",
      mfa: true,
      permissions: [],
      memberships: [],
      role: "owner",
      user: {
        id: "u1",
        email: "a@b.c",
        name: "Ada",
        locale: "en",
        instanceRole: "owner",
        status: "active",
      },
    };

    const { useSettings } = await import("@/features/settings/composables/useSettings");
    const s = useSettings();
    await s.load();
    s.tab.value = "switches";
    s.setSwitchDraft("sandbox.enabled", true);
    expect(s.dirtySwitchKeys.value).toContain("sandbox.enabled");
    expect(s.isDirty.value).toBe(true);
    s.discard();
    expect(s.isDirty.value).toBe(false);
    expect(s.draftSwitch("sandbox.enabled")).toBe(false);
  });
});
