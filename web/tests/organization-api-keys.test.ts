import { beforeEach, describe, expect, it, vi } from "vitest";

const listApiKeys = vi.fn();
const createApiKey = vi.fn();
const patchApiKey = vi.fn();
const revokeApiKey = vi.fn();

const getAccount = vi.fn(async (id: string) => ({
  id,
  name: id === "acc_1" ? "Support" : id,
  email: "support@example.test",
}));

vi.mock("@/api/apikeys", () => ({
  listApiKeys: (...args: unknown[]) => listApiKeys(...args),
  createApiKey: (...args: unknown[]) => createApiKey(...args),
  patchApiKey: (...args: unknown[]) => patchApiKey(...args),
  revokeApiKey: (...args: unknown[]) => revokeApiKey(...args),
  getAccount: (...args: unknown[]) => getAccount(...args),
}));

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

describe("useApiKeys", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listApiKeys.mockResolvedValue({
      data: [
        {
          id: "key_1",
          name: "CI",
          prefix: "eb_live_",
          last4: "abcd",
          scopes: ["accounts:read"],
          organizationId: "org_1",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 2,
        },
      ],
      nextCursor: null,
      total: 1,
    });
  });

  it("loads keys, hides revoked, resolves account names, and searches via q", async () => {
    listApiKeys.mockResolvedValueOnce({
      data: [
        {
          id: "key_1",
          name: "CI",
          prefix: "eb_live_",
          last4: "abcd",
          scopes: ["admin"],
          accountId: "acc_1",
          organizationId: "org_1",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
        },
        {
          id: "key_2",
          name: "Old",
          prefix: "eb_live_",
          last4: "zzzz",
          scopes: ["admin"],
          organizationId: "org_1",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
          revokedAt: "2026-02-01T00:00:00Z",
        },
      ],
      nextCursor: null,
      total: 2,
    });
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    await k.load();
    expect(k.items.value).toHaveLength(1);
    expect(k.items.value[0].prefix).toBe("eb_live_…abcd");
    expect(k.items.value[0].accountName).toBe("Support");

    k.onSearch("CI");
    await vi.waitFor(() => expect(listApiKeys).toHaveBeenCalled());
    const arg = listApiKeys.mock.calls.at(-1)?.[0] as { q?: string };
    expect(arg.q).toBe("CI");
  });

  it("never renders a missing prefix or last4 as the literal \"undefined\"", async () => {
    listApiKeys.mockResolvedValueOnce({
      data: [
        {
          id: "key_9",
          name: "Legacy",
          scopes: ["read"],
          organizationId: "org_1",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
        },
      ],
      nextCursor: null,
      total: 1,
    });
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    await k.load();
    expect(k.items.value[0].prefix).toBe("—");
    expect(k.items.value[0].prefix).not.toContain("undefined");
  });

  it("maskedKey joins prefix and last4 and drops blank parts", async () => {
    const { maskedKey } = await import("@/features/organization/helpers/maskedKey");
    expect(maskedKey("ebk_", "9f2c")).toBe("ebk_…9f2c");
    expect(maskedKey("ebk_", undefined)).toBe("ebk_…");
    expect(maskedKey(undefined, "9f2c")).toBe("…9f2c");
    expect(maskedKey(null, "  ")).toBe("—");
    expect(maskedKey(undefined, undefined)).toBe("—");
  });

  it("stores the created token once", async () => {
    createApiKey.mockResolvedValueOnce({
      id: "key_new",
      name: "Bot",
      prefix: "eb_live_",
      last4: "ffff",
      scopes: ["admin"],
      organizationId: "org_1",
      createdAt: "2026-10-26T00:00:00Z",
      updatedAt: "2026-10-26T00:00:00Z",
      version: 1,
      token: "eb_live_secret_once",
    });
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    const created = await k.create({ name: "Bot", scopes: ["admin"] });
    expect(created?.token).toBe("eb_live_secret_once");
    expect(k.createdToken.value?.token).toBe("eb_live_secret_once");
    k.clearCreatedToken();
    expect(k.createdToken.value).toBeNull();
  });

  it("updates and revokes keys", async () => {
    patchApiKey.mockResolvedValueOnce({});
    revokeApiKey.mockResolvedValueOnce(undefined);
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    await k.load();
    const row = k.items.value[0];
    expect(await k.update(row, { name: "CI2" })).toBe(true);
    expect(await k.revoke(row)).toBe(true);
  });

  it("marks 403 as forbidden and handles create failure", async () => {
    const { ApiProblemError } = await import("@/api/client");
    listApiKeys.mockRejectedValueOnce(
      new ApiProblemError({ status: 403, code: "forbidden", title: "no" }),
    );
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    await k.load();
    expect(k.forbidden.value).toBe(true);

    createApiKey.mockRejectedValueOnce(new Error("fail"));
    expect(await k.create({ name: "x", scopes: ["admin"] })).toBeNull();
  });
});

