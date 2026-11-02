import { beforeEach, describe, expect, it, vi } from "vitest";
import { useGateways, sortParam } from "@/features/gateways/composables/useGateways";
import {
  authResultColor,
  folderLabelKey,
  isAPIKind,
  kindLabelKey,
  policyLabelKey,
  stateLabelKey,
  tlsLabelKey,
} from "@/features/gateways/helpers/labels";

vi.mock("@/api/gateways", () => ({
  listSmtpGateways: vi.fn(),
  createSmtpGateway: vi.fn(),
  patchSmtpGateway: vi.fn(),
  deleteSmtpGateway: vi.fn(),
  verifySmtpGateway: vi.fn(),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import {
  createSmtpGateway,
  deleteSmtpGateway,
  listSmtpGateways,
  patchSmtpGateway,
  verifySmtpGateway,
} from "@/api/gateways";

const sample = {
  id: "gwy_1",
  organizationId: "org_1",
  name: "Relay",
  kind: "smtp",
  host: "smtp.test",
  port: 465,
  tls: "implicit",
  auth: { user: "u", hasPassword: true },
  enabled: true,
  createdAt: "2026-10-01T00:00:00Z",
  updatedAt: "2026-10-25T00:00:00Z",
  version: 1,
};

describe("useGateways", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sortParam", () => {
    expect(sortParam([])).toBe("-createdAt");
    expect(sortParam([{ key: "createdAt", order: "asc" }])).toBe("createdAt");
  });

  it("loads CRUD verify toggle", async () => {
    vi.mocked(listSmtpGateways).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(createSmtpGateway).mockResolvedValue(sample);
    vi.mocked(patchSmtpGateway).mockResolvedValue({ ...sample, enabled: false });
    vi.mocked(deleteSmtpGateway).mockResolvedValue(undefined);
    vi.mocked(verifySmtpGateway).mockResolvedValue({ ok: true, latencyMs: 20, capabilities: ["AUTH"] });

    const w = useGateways();
    await w.load();
    expect(w.rows.value[0]?.name).toBe("Relay");

    await w.create({
      name: "Relay",
      host: "smtp.test",
      port: 465,
      tls: "implicit",
      auth: { user: "u", pass: "p" },
    });
    await w.setEnabled(sample, false);
    expect(patchSmtpGateway).toHaveBeenCalledWith("gwy_1", { enabled: false });
    await w.runVerify("gwy_1");
    expect(w.verifyResult.value?.ok).toBe(true);
    expect(await w.remove("gwy_1")).toBe(true);
    w.clearVerify();
    expect(w.verifyResult.value).toBeNull();
  });
});

describe("gateway labels", () => {
  it("maps enums", () => {
    expect(tlsLabelKey("starttls")).toContain("starttls");
    expect(policyLabelKey("atMostOnce")).toContain("atMostOnce");
    expect(stateLabelKey("timedOut")).toContain("timedOut");
    expect(folderLabelKey("spam")).toContain("spam");
    expect(authResultColor("pass")).toBe("success");
    expect(authResultColor("fail")).toBe("error");
    expect(kindLabelKey("mailgun")).toContain("mailgun");
    expect(isAPIKind("ses")).toBe(true);
    expect(isAPIKind("smtp")).toBe(false);
  });
});
