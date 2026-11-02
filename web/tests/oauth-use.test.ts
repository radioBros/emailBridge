import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("@/stores/notify", () => ({
  useNotifyStore: () => ({ push: vi.fn() }),
}));

const listOauthApps = vi.fn();
const createOauthApp = vi.fn();
const patchOauthApp = vi.fn();
const deleteOauthApp = vi.fn();
const verifyOauthApp = vi.fn();

vi.mock("@/api/oauth", () => ({
  listOauthApps: (...a: unknown[]) => listOauthApps(...a),
  createOauthApp: (...a: unknown[]) => createOauthApp(...a),
  patchOauthApp: (...a: unknown[]) => patchOauthApp(...a),
  deleteOauthApp: (...a: unknown[]) => deleteOauthApp(...a),
  verifyOauthApp: (...a: unknown[]) => verifyOauthApp(...a),
}));

import { useOauthApps } from "@/features/oauth/composables/useOauthApps";

const sample = {
  id: "oap_01",
  organizationId: "org_1",
  name: "Gmail",
  provider: "gmail" as const,
  clientId: "cid",
  hasClientSecret: true,
  scopes: ["mail"],
  enabled: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  version: 1,
};

describe("useOauthApps", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    listOauthApps.mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
  });

  it("loads rows", async () => {
    const c = useOauthApps();
    await c.load();
    expect(c.items.value).toHaveLength(1);
    expect(c.items.value[0].name).toBe("Gmail");
    expect(c.total.value).toBe(1);
  });

  it("creates and reloads", async () => {
    createOauthApp.mockResolvedValue(sample);
    const c = useOauthApps();
    const ok = await c.create({
      name: "Gmail",
      provider: "gmail",
      clientId: "cid",
      clientSecret: "sec",
    });
    expect(ok).toBe(true);
    expect(createOauthApp).toHaveBeenCalled();
    expect(listOauthApps).toHaveBeenCalled();
  });

  it("updates and verifies", async () => {
    patchOauthApp.mockResolvedValue({ ...sample, version: 2 });
    verifyOauthApp.mockResolvedValue({ ok: true, latencyMs: 12 });
    const c = useOauthApps();
    await c.load();
    const row = c.items.value[0];
    expect(await c.update(row, { name: "G2" })).toBe(true);
    const vr = await c.verify(row);
    expect(vr?.ok).toBe(true);
  });

  it("removes a row", async () => {
    deleteOauthApp.mockResolvedValue(undefined);
    const c = useOauthApps();
    await c.load();
    await c.remove(c.items.value[0]);
    expect(deleteOauthApp).toHaveBeenCalledWith("oap_01", 1);
  });

  it("handles load and mutate errors", async () => {
    const { ApiProblemError } = await import("@/api/client");
    listOauthApps.mockRejectedValueOnce(
      new ApiProblemError({ status: 403, code: "forbidden", title: "no" }),
    );
    const c = useOauthApps();
    await c.load();
    expect(c.forbidden.value).toBe(true);

    listOauthApps.mockRejectedValueOnce(new Error("boom"));
    await c.load();
    expect(c.loadError.value).toBeTruthy();

    createOauthApp.mockRejectedValueOnce(new Error("fail"));
    expect(await c.create({
      name: "x",
      provider: "gmail",
      clientId: "c",
      clientSecret: "s",
    })).toBe(false);

    listOauthApps.mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    await c.load();
    patchOauthApp.mockRejectedValueOnce(new Error("fail"));
    expect(await c.update(c.items.value[0], { name: "y" })).toBe(false);

    deleteOauthApp.mockRejectedValueOnce(new Error("fail"));
    await c.remove(c.items.value[0]);

    verifyOauthApp.mockRejectedValueOnce(new Error("fail"));
    expect(await c.verify(c.items.value[0])).toBeNull();

    verifyOauthApp.mockResolvedValueOnce({ ok: false, latencyMs: 1, detail: "bad" });
    expect((await c.verify(c.items.value[0]))?.ok).toBe(false);

    c.onSearch("g");
    c.setPage(2);
    c.setIpp(10);
    c.setSortBy([{ key: "name", order: "asc" }]);
  });
});
