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

const listIntegrations = vi.fn();
const listIntegrationInstances = vi.fn();
const installIntegration = vi.fn();
const patchIntegrationInstance = vi.fn();
const deleteIntegrationInstance = vi.fn();
const testIntegrationInstance = vi.fn();

vi.mock("@/api/integrations", () => ({
  listIntegrations: (...a: unknown[]) => listIntegrations(...a),
  listIntegrationInstances: (...a: unknown[]) => listIntegrationInstances(...a),
  installIntegration: (...a: unknown[]) => installIntegration(...a),
  patchIntegrationInstance: (...a: unknown[]) => patchIntegrationInstance(...a),
  deleteIntegrationInstance: (...a: unknown[]) => deleteIntegrationInstance(...a),
  testIntegrationInstance: (...a: unknown[]) => testIntegrationInstance(...a),
}));

import { useIntegrations } from "@/features/integrations/composables/useIntegrations";

const slack = {
  id: "slack",
  version: "1.0.0",
  name: "Slack",
  category: "chat",
  configSchema: { type: "object", properties: {} },
  secretFields: [] as string[],
  capabilities: ["test"],
  installedCount: 1,
  instanceIds: ["int_1"],
};

const instance = {
  id: "int_1",
  organizationId: "org_1",
  type: "slack",
  definitionVersion: "1.0.0",
  name: "Slack",
  config: { webhookUrl: "https://hooks.example/x" },
  secretsSet: {},
  createdResources: { endpoints: ["whe_1"], routes: ["rte_1"] },
  enabled: true,
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  version: 1,
  lastActionAt: "2026-01-02T00:00:00Z",
  lastActionOK: true,
  lastActionDetail: "ok",
};

describe("useIntegrations", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    listIntegrations.mockResolvedValue({ data: [slack] });
    listIntegrationInstances.mockResolvedValue({
      data: [instance],
      nextCursor: null,
      total: 1,
    });
  });

  it("loads catalogue and instances", async () => {
    const c = useIntegrations();
    await c.load();
    expect(c.catalogue.value).toHaveLength(1);
    expect(c.rows.value).toHaveLength(1);
    expect(c.rows.value[0].name).toBe("Slack");
    expect(c.rows.value[0].lastActionOK).toBe(true);
    expect(c.rows.value[0].lastActionAt).toBe("2026-01-02T00:00:00Z");
    expect(c.groupedCatalogue.value[0].category).toBe("chat");
    expect(c.typeName("slack")).toBe("Slack");
    expect(c.typeName("unknown")).toBe("unknown");
  });

  it("installs and reloads", async () => {
    installIntegration.mockResolvedValue(instance);
    const c = useIntegrations();
    const ok = await c.install("slack", {
      config: { webhookUrl: "https://hooks.example/x" },
    });
    expect(ok).toBe(true);
    expect(installIntegration).toHaveBeenCalled();
    expect(listIntegrations).toHaveBeenCalled();
  });

  it("updates enables and tests", async () => {
    patchIntegrationInstance.mockResolvedValue({ ...instance, version: 2, enabled: false });
    testIntegrationInstance.mockResolvedValue({ ok: true, latencyMs: 12 });
    const c = useIntegrations();
    await c.load();
    const row = c.rows.value[0];
    expect(await c.setEnabled(row, false)).toBe(true);
    expect(patchIntegrationInstance).toHaveBeenCalled();
    const tr = await c.runTest(row);
    expect(tr?.ok).toBe(true);
  });

  it("removes an instance", async () => {
    deleteIntegrationInstance.mockResolvedValue(undefined);
    const c = useIntegrations();
    await c.load();
    expect(await c.remove(c.rows.value[0])).toBe(true);
    expect(deleteIntegrationInstance).toHaveBeenCalled();
  });

  it("handles catalogue search and pagination helpers", async () => {
    const c = useIntegrations();
    c.onCatalogueSearch("slack");
    await Promise.resolve();
    expect(listIntegrations).toHaveBeenCalledWith({ q: "slack" });
    c.onSearch("x");
    c.setPage(2);
    c.setIpp(10);
    c.setSortBy([{ key: "name", order: "asc" }]);
    expect(c.page.value).toBe(2);
  });

  it("surfaces load errors", async () => {
    listIntegrations.mockRejectedValue(new Error("boom"));
    listIntegrationInstances.mockRejectedValue(new Error("boom"));
    const c = useIntegrations();
    await c.load();
    expect(c.catalogueError.value || c.loadError.value).toBeTruthy();
  });
});
