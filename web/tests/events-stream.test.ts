import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { ref } from "vue";
import type { EbEvent } from "@/api/events";

const fetchMock = vi.fn();
vi.stubGlobal("fetch", fetchMock);

vi.mock("@/api/events", async () => {
  const actual = await vi.importActual<typeof import("@/api/events")>("@/api/events");
  return {
    ...actual,
    eventsStreamPath: vi.fn((q: { type?: string; accountId?: string; lastEventId?: string }) => {
      const p = new URLSearchParams();
      if (q.type) p.set("type", q.type);
      if (q.accountId) p.set("accountId", q.accountId);
      if (q.lastEventId) p.set("lastEventId", q.lastEventId);
      const s = p.toString();
      return `/v1/events/stream${s ? `?${s}` : ""}`;
    }),
  };
});

vi.mock("@/stores/session", () => ({
  useSessionStore: () => ({
    organizationId: "org_1",
    me: { activeOrganizationId: "org_1" },
  }),
}));

const sample: EbEvent = {
  specversion: "1.0",
  id: "evt_live",
  type: "message.received",
  source: "/organizations/org_1",
  subject: "msg_1",
  time: "2026-10-26T10:00:00Z",
  datacontenttype: "application/json",
  dataschema: "https://x",
  eborg: "org_1",
  ebaccount: "acc_1",
  ebschema: 1,
  data: {},
};

const sseBody = (chunks: string[]) => {
  let i = 0;
  return {
    getReader: () => ({
      read: async () => {
        if (i >= chunks.length) return { done: true, value: undefined };
        const value = new TextEncoder().encode(chunks[i++]);
        return { done: false, value };
      },
    }),
  };
};

describe("useEventStream", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    fetchMock.mockReset();
    Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
  });

  it("prepends SSE events and stops", async () => {
    fetchMock.mockResolvedValue({
      ok: true,
      body: sseBody([
        `id: evt_live\nevent: message.received\ndata: ${JSON.stringify(sample)}\n\n`,
        ":keepalive\n\n",
      ]),
    });
    const { useEventStream } = await import("@/features/events/composables/useEventStream");
    const rows = ref<EbEvent[]>([]);
    const stream = useEventStream(rows);
    const run = stream.start({ type: "message.*" });
    await vi.waitFor(() => expect(rows.value.some((r) => r.id === "evt_live")).toBe(true));
    stream.stop();
    await run;
    expect(fetchMock).toHaveBeenCalled();
    expect(stream.live.value).toBe(false);
  });

  it("records stream HTTP errors", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 403, body: null });
    const { useEventStream } = await import("@/features/events/composables/useEventStream");
    const rows = ref<EbEvent[]>([]);
    const stream = useEventStream(rows);
    await stream.start({});
    expect(stream.error.value).toContain("403");
  });
});
