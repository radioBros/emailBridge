import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { ref } from "vue";
import type { LogRecord } from "@/api/logs";

vi.mock("@/stores/session", () => ({
  useSessionStore: () => ({
    organizationId: "org_1",
    me: { activeOrganizationId: "org_1" },
  }),
}));

describe("useLogTail", () => {
  beforeEach(() => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        const encoder = new TextEncoder();
        const stream = new ReadableStream({
          start(controller) {
            controller.enqueue(
              encoder.encode(
                `data: ${JSON.stringify({
                  id: "log_01",
                  time: 1,
                  level: "info",
                  category: "api",
                  component: "http",
                  msg: "hello",
                })}\n\n`,
              ),
            );
            controller.close();
          },
        });
        return {
          ok: true,
          body: stream,
          status: 200,
        };
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("prepends SSE records and respects the cap", async () => {
    const { useLogTail } = await import("@/features/logs/composables/useLogTail");
    const rows = ref<LogRecord[]>([]);
    const tail = useLogTail(rows);
    const run = tail.start({});
    await vi.waitFor(() => expect(rows.value.length).toBe(1));
    expect(rows.value[0]?.msg).toBe("hello");
    expect(tail.cap).toBe(500);
    tail.stop();
    await run;
  });

  it("buffers while paused", async () => {
    const { useLogTail } = await import("@/features/logs/composables/useLogTail");
    const rows = ref<LogRecord[]>([]);
    const tail = useLogTail(rows);
    tail.setPaused(true);
    const run = tail.start({});
    await new Promise((r) => setTimeout(r, 30));
    expect(rows.value.length).toBe(0);
    tail.setPaused(false);
    await vi.waitFor(() => expect(rows.value.length).toBe(1));
    tail.stop();
    await run;
  });
});
