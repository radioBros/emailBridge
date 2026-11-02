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

const getSandboxMessage = vi.fn();
const getSandboxMessageRaw = vi.fn();
const deleteSandboxMessage = vi.fn();
const downloadSandboxRaw = vi.fn();

vi.mock("@/api/sandbox", () => ({
  getSandboxMessage: (...args: unknown[]) => getSandboxMessage(...args),
  getSandboxMessageRaw: (...args: unknown[]) => getSandboxMessageRaw(...args),
  deleteSandboxMessage: (...args: unknown[]) => deleteSandboxMessage(...args),
  downloadSandboxRaw: (...args: unknown[]) => downloadSandboxRaw(...args),
}));

import { useSandboxViewer } from "@/features/sandbox/composables/useSandboxViewer";

const detail = {
  id: "cap_1",
  organizationId: "org_1",
  accountId: "acc_1",
  outboundId: "out_1",
  envelope: { from: "a@test", to: ["b@test"] },
  subject: "Hi",
  capturedAt: "2026-10-26T11:00:00Z",
};

const rawText = [
  "From: a@test",
  "To: b@test",
  "Subject: Hi",
  "Content-Type: text/plain",
  "",
  "body",
].join("\r\n");

describe("useSandboxViewer", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads detail and parses raw mime", async () => {
    getSandboxMessage.mockResolvedValue(detail);
    getSandboxMessageRaw.mockResolvedValue({
      ok: true,
      bytes: new TextEncoder().encode(rawText).buffer,
      contentType: "message/rfc822",
    });

    const v = useSandboxViewer();
    await v.show("cap_1");
    expect(v.detail.value?.id).toBe("cap_1");
    expect(v.parsed.value?.text).toContain("body");
    expect(v.hasText.value).toBe(true);
  });

  it("handles missing raw body", async () => {
    getSandboxMessage.mockResolvedValue(detail);
    getSandboxMessageRaw.mockResolvedValue({ ok: false, missing: true });
    const v = useSandboxViewer();
    await v.show("cap_1");
    expect(v.rawMissing.value).toBe(true);
  });

  it("deletes and downloads", async () => {
    getSandboxMessage.mockResolvedValue(detail);
    getSandboxMessageRaw.mockResolvedValue({ ok: false, missing: true });
    deleteSandboxMessage.mockResolvedValue(undefined);
    downloadSandboxRaw.mockResolvedValue({ ok: false, missing: true });

    const v = useSandboxViewer();
    await v.show("cap_1");
    await v.download();
    expect(downloadSandboxRaw).toHaveBeenCalled();
    const removed = await v.remove();
    expect(removed).toBe(true);
    expect(v.open.value).toBe(false);
  });
});
