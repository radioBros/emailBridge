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

describe("useApiKeys (P3.4a)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listApiKeys.mockResolvedValue({
      data: [
        {
          id: "key_1",
          name: "CI",
          prefix: "ebk_",
          last4: "abcd",
          scopes: ["accounts:write:acc_1"],
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

  it("loads keys with granular scopes", async () => {
    const { useApiKeys } = await import("@/features/organization/composables/useApiKeys");
    const k = useApiKeys();
    await k.load();
    expect(k.items.value[0].scopes).toEqual(["accounts:write:acc_1"]);
    expect(listApiKeys).toHaveBeenCalled();
  });
});
