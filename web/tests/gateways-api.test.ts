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
  createSmtpGateway,
  deleteSmtpGateway,
  getSmtpGateway,
  listSmtpGateways,
  patchSmtpGateway,
  verifySmtpGateway,
} from "@/api/gateways";
import {
  createDeliveryTest,
  getDeliveryTest,
  isTerminalDeliveryTest,
} from "@/api/deliveryTests";

describe("gateways api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists and verifies", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [], total: 0 });
    vi.mocked(api.post).mockResolvedValue({ ok: true, latencyMs: 12 });
    await listSmtpGateways({ page: 1, q: "relay" });
    await verifySmtpGateway("gwy_1");
    expect(api.get).toHaveBeenCalledWith(expect.stringContaining("/smtp-gateways"));
    expect(api.post).toHaveBeenCalledWith("/smtp-gateways/gwy_1/verify");
  });

  it("CRUD paths", async () => {
    vi.mocked(api.get).mockResolvedValue({ id: "gwy_1" });
    vi.mocked(api.post).mockResolvedValue({ id: "gwy_1" });
    vi.mocked(api.patch).mockResolvedValue({ id: "gwy_1" });
    vi.mocked(api.delete).mockResolvedValue(undefined);
    await getSmtpGateway("gwy_1");
    await createSmtpGateway({
      name: "Relay",
      host: "smtp.test",
      port: 465,
      tls: "implicit",
      auth: { user: "u", pass: "p" },
    });
    await patchSmtpGateway("gwy_1", { enabled: false });
    await deleteSmtpGateway("gwy_1");
    expect(api.delete).toHaveBeenCalledWith("/smtp-gateways/gwy_1");
  });
});

describe("deliveryTests api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("creates and gets", async () => {
    vi.mocked(api.post).mockResolvedValue({ id: "dtt_1", jobId: "job_1", state: "queued" });
    vi.mocked(api.get).mockResolvedValue({ id: "dtt_1", state: "succeeded" });
    await createDeliveryTest({ accountId: "acc_1", to: "seed@test" });
    await getDeliveryTest("dtt_1");
    expect(isTerminalDeliveryTest("succeeded")).toBe(true);
    expect(isTerminalDeliveryTest("running")).toBe(false);
  });
});
