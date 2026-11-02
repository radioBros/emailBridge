import { createServer } from "node:http";
import { describe, expect, it } from "vitest";
import { Client, streamEvents, type SSEEvent } from "../index.js";

describe("streamEvents", () => {
  it("parses SSE and sends Last-Event-ID", async () => {
    let gotLast = "";
    const server = createServer((req, res) => {
      gotLast = String(req.headers["last-event-id"] ?? "");
      res.writeHead(200, { "content-type": "text/event-stream" });
      res.write("id: evt_1\nevent: message.received\ndata: {\"id\":\"evt_1\"}\n\n");
      res.write(":keepalive\n\n");
      res.end();
    });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    const addr = server.address();
    if (!addr || typeof addr === "string") throw new Error("no addr");
    const baseUrl = `http://127.0.0.1:${addr.port}`;
    const c = new Client({ baseUrl, apiKey: "ebk_x" });
    const events: SSEEvent[] = [];
    await streamEvents(c, {
      lastEventId: "evt_0",
      onEvent: (ev) => {
        events.push(ev);
      },
    });
    server.close();
    expect(gotLast).toBe("evt_0");
    expect(events).toHaveLength(1);
    expect(events[0].id).toBe("evt_1");
    expect(events[0].event).toBe("message.received");
  });
});
