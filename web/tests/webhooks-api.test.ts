import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import { api } from "@/api/client";
import {
  createWebhookEndpoint,
  deleteWebhookEndpoint,
  getWebhookEndpoint,
  listWebhookEndpoints,
  patchWebhookEndpoint,
  rotateWebhookEndpointSecret,
  testWebhookEndpoint,
} from "@/api/webhooks";

describe("webhooks api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists with query params", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [], nextCursor: null, total: 0 });
    await listWebhookEndpoints({ page: 2, limit: 10, sort: "name" });
    expect(api.get).toHaveBeenCalledWith(
      expect.stringMatching(/\/webhook-endpoints\?.*page=2.*limit=10/),
    );
  });

  it("CRUD + rotate + test paths", async () => {
    vi.mocked(api.get).mockResolvedValue({ id: "whe_1" });
    vi.mocked(api.post).mockResolvedValue({ id: "whe_1" });
    vi.mocked(api.patch).mockResolvedValue({ id: "whe_1" });
    vi.mocked(api.delete).mockResolvedValue(undefined);
    await getWebhookEndpoint("whe_1");
    await createWebhookEndpoint({ name: "n", kind: "http", events: ["*"] });
    await patchWebhookEndpoint("whe_1", { enabled: false });
    await deleteWebhookEndpoint("whe_1");
    await rotateWebhookEndpointSecret("whe_1");
    await testWebhookEndpoint("whe_1");
    expect(api.get).toHaveBeenCalledWith("/webhook-endpoints/whe_1");
    expect(api.post).toHaveBeenCalledWith("/webhook-endpoints/whe_1/rotate-secret");
    expect(api.post).toHaveBeenCalledWith("/webhook-endpoints/whe_1/test");
    expect(api.delete).toHaveBeenCalledWith("/webhook-endpoints/whe_1");
  });
});
