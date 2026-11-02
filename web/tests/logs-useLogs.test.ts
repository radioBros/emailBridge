import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({ messageOf: () => "err", fieldErrors: () => ({}) }),
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
    permissions: ["logs:read"],
    memberships: [],
    role: "admin",
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

vi.mock("@/api/logs", () => ({
  listLogs: vi.fn(async () => ({
    data: [
      {
        id: "log_01j000000000000000000000",
        time: 1_700_000_000_000_000_000,
        level: "info",
        category: "api",
        component: "http",
        msg: "ok",
        requestId: "req_1",
      },
    ],
    nextCursor: "cur_2",
  })),
  getLogStats: vi.fn(async () => ({
    from: "2026-01-01T00:00:00Z",
    to: "2026-01-02T00:00:00Z",
    byCategory: { api: 1 },
    byLevel: { info: 1 },
    disk: { api: { bytes: 2048, nodes: { n1: 2048 } } },
  })),
  getLogSettings: vi.fn(async () => ({ body: { stdout: "off" }, etag: "v1" })),
  patchLogSettings: vi.fn(async () => ({ body: { stdout: "json" }, etag: "v2" })),
  exportLogs: vi.fn(async () => new Blob(["{}\n"])),
  searchAccounts: vi.fn(async () => [{ id: "acc_1", name: "Ada" }]),
  logsStreamPath: () => "/v1/logs/stream",
}));

vi.stubGlobal("URL", {
  createObjectURL: () => "blob:mock",
  revokeObjectURL: vi.fn(),
});

const click = vi.fn();
vi.spyOn(document, "createElement").mockImplementation(((tag: string) => {
  if (tag === "a") {
    return { href: "", download: "", click } as unknown as HTMLAnchorElement;
  }
  return document.createElement(tag);
}) as typeof document.createElement);

describe("useLogs", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  const seedSession = async (): Promise<void> => {
    const { useSessionStore } = await import("@/stores/session");
    const session = useSessionStore();
    session.me = {
      kind: "user",
      mfa: true,
      permissions: ["logs:read"],
      memberships: [],
      role: "admin",
      user: {
        id: "u1",
        email: "a@b.c",
        name: "Ada",
        locale: "en",
        instanceRole: "owner",
        status: "active",
      },
    };
  };

  it("loads a cursor page and disk cards", async () => {
    await seedSession();
    const { useLogs } = await import("@/features/logs/composables/useLogs");
    const logs = useLogs();
    await logs.load();

    expect(logs.items.value).toHaveLength(1);
    expect(logs.total.value).toBe(51);
    expect(logs.diskCards.value[0]?.category).toBe("api");
    expect(logs.diskCards.value[0]?.bytes).toBe(2048);
  });

  it("opens detail, settings, export and pagination helpers", async () => {
    await seedSession();
    const api = await import("@/api/logs");
    const { useLogs } = await import("@/features/logs/composables/useLogs");
    const logs = useLogs();
    await logs.load();

    const row = logs.items.value[0]!;
    logs.openDetail(row);
    expect(logs.drawerOpen.value).toBe(true);
    expect(logs.selected.value?.id).toBe(row.id);

    await logs.openSettings();
    expect(logs.settingsOpen.value).toBe(true);
    expect(logs.logSettings.value?.stdout).toBe("off");

    logs.logSettings.value = { stdout: "json" };
    await logs.saveSettings();
    expect(api.patchLogSettings).toHaveBeenCalled();

    await logs.doExport();
    expect(api.exportLogs).toHaveBeenCalled();

    logs.setIpp(25);
    await Promise.resolve();
    expect(logs.ipp.value).toBe(25);

    logs.setSearch("fail");
    expect(logs.search.value).toBe("fail");

    await logs.loadAccounts("ada");
    expect(api.searchAccounts).toHaveBeenCalled();
  });
});
