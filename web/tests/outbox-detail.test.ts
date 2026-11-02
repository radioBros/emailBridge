import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => (err instanceof Error ? err.message : String(err)),
    fieldErrors: () => ({}),
  }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (key: string) => key,
    te: () => true,
    locale: { value: "en" },
  }),
}));

const getOutboundMessage = vi.fn();
const cancelOutboundMessage = vi.fn();

vi.mock("@/api/outbox", () => ({
  getOutboundMessage: (...args: unknown[]) => getOutboundMessage(...args),
  cancelOutboundMessage: (...args: unknown[]) => cancelOutboundMessage(...args),
}));

import { useOutboxDetail } from "@/features/outbox/composables/useOutboxDetail";

const sample = {
  id: "out_1",
  organizationId: "org_1",
  accountId: "acc_1",
  state: "queued",
  policy: "standard",
  messageId: "<m@test>",
  messageIdHeader: "<m@test>",
  envelope: { from: "a@test", to: ["b@test"] },
  copyToSent: { enabled: true, state: "pending" },
  createdAt: "2026-10-26T10:00:00Z",
  size: 100,
  maxAttempts: 5,
  version: 1,
  subject: "Hello",
  attempts: [],
};

describe("useOutboxDetail", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads detail and cancels when allowed", async () => {
    getOutboundMessage.mockResolvedValue(sample);
    cancelOutboundMessage.mockResolvedValue({ ...sample, state: "cancelled" });

    const d = useOutboxDetail();
    await d.show("out_1");
    expect(d.detail.value?.id).toBe("out_1");
    expect(d.canCancel.value).toBe(true);

    const ok = await d.cancel();
    expect(ok).toBe(true);
    expect(d.detail.value?.state).toBe("cancelled");
    d.close();
    expect(d.open.value).toBe(false);
  });

  it("surfaces load errors", async () => {
    getOutboundMessage.mockRejectedValue(new Error("boom"));
    const d = useOutboxDetail();
    await d.show("out_1");
    expect(d.loadError.value).toContain("boom");
  });
});
