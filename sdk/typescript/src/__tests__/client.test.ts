import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Client, ProblemError } from "../index.js";

let baseUrl = "";
let server: ReturnType<typeof createServer>;
let hits = 0;
let lastHeaders: IncomingMessage["headers"] = {};

beforeAll(async () => {
  hits = 0;
  server = createServer(async (req: IncomingMessage, res: ServerResponse) => {
    hits++;
    lastHeaders = req.headers;
    const url = new URL(req.url ?? "/", "http://127.0.0.1");

    if (url.pathname === "/v1/accounts") {
      if (req.method === "POST") {
        res.writeHead(201, { "content-type": "application/json" });
        res.end(JSON.stringify({ id: "acc_new" }));
        return;
      }
      const cursor = url.searchParams.get("cursor");
      if (!cursor) {
        res.writeHead(200, { "content-type": "application/json" });
        res.end(JSON.stringify({ data: [{ id: "acc_1" }], nextCursor: "c2" }));
        return;
      }
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ data: [{ id: "acc_2" }], nextCursor: null }));
      return;
    }

    if (url.pathname === "/v1/boom") {
      res.writeHead(422, { "content-type": "application/problem+json" });
      res.end(
        JSON.stringify({
          type: "https://emailbridge.dev/problems/validation_failed",
          title: "Validation failed",
          status: 422,
          code: "validation_failed",
          kind: "user",
          detail: "bad",
        }),
      );
      return;
    }

    if (url.pathname === "/v1/slow") {
      if (hits <= 2) {
        res.writeHead(429, { "Retry-After": "0", "content-type": "application/problem+json" });
        res.end(
          JSON.stringify({
            type: "https://emailbridge.dev/problems/rate_limited",
            title: "Rate limited",
            status: 429,
            code: "rate_limited",
            kind: "rate_limited",
          }),
        );
        return;
      }
      res.writeHead(200, { "content-type": "application/json" });
      res.end(JSON.stringify({ ok: true }));
      return;
    }

    res.writeHead(404);
    res.end();
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  const addr = server.address();
  if (!addr || typeof addr === "string") throw new Error("no addr");
  baseUrl = `http://127.0.0.1:${addr.port}`;
});

afterAll(async () => {
  await new Promise<void>((r) => server.close(() => r()));
});

describe("Client", () => {
  it("sends auth and org headers", async () => {
    hits = 0;
    const c = new Client({ baseUrl, apiKey: "ebk_test", organizationId: "org_1" });
    await c.get("/v1/accounts");
    expect(lastHeaders.authorization).toBe("Bearer ebk_test");
    expect(lastHeaders["eb-organization"]).toBe("org_1");
  });

  it("sends Idempotency-Key", async () => {
    const c = new Client({ baseUrl, apiKey: "ebk_test" });
    await c.post("/v1/accounts", { body: {}, idempotencyKey: "idem-1" });
    expect(lastHeaders["idempotency-key"]).toBe("idem-1");
  });

  it("throws ProblemError", async () => {
    const c = new Client({ baseUrl, apiKey: "ebk_test" });
    await expect(c.get("/v1/boom")).rejects.toBeInstanceOf(ProblemError);
    try {
      await c.get("/v1/boom");
    } catch (e) {
      expect(e).toBeInstanceOf(ProblemError);
      const pe = e as ProblemError;
      expect(pe.problem.code).toBe("validation_failed");
      expect(pe.status).toBe(422);
    }
  });

  it("retries 429 with Retry-After", async () => {
    hits = 0;
    const c = new Client({ baseUrl, apiKey: "ebk_test", maxRetries: 3 });
    const out = await c.get<{ ok: boolean }>("/v1/slow");
    expect(out.ok).toBe(true);
    expect(hits).toBeGreaterThanOrEqual(3);
  });

  it("iterates cursor pages", async () => {
    const c = new Client({ baseUrl, apiKey: "ebk_test" });
    const ids: string[] = [];
    for await (const row of c.iterateCursor<{ id: string }>("/v1/accounts")) {
      ids.push(row.id);
    }
    expect(ids).toEqual(["acc_1", "acc_2"]);
  });
});
