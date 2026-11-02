# `@emailbridge/sdk`

TypeScript SDK for the EmailBridge API: typed `fetch` client, Standard Webhooks /
`bodyOnly` verifier, and SSE event-stream helper.

Requires Node ≥ 18 (or any runtime with `fetch` + Web Crypto).

## Install

```bash
npm install @emailbridge/sdk
```

## Quick start

```ts
import { Client, verify, streamEvents } from "@emailbridge/sdk";

const client = new Client({
  baseUrl: "https://mail.example.com",
  apiKey: process.env.EB_API_KEY!,
  organizationId: "org_…", // optional for session-style callers; API keys imply org
});

const accounts = await client.get("/v1/accounts", { query: { limit: 50 } });

for await (const row of client.iterateCursor("/v1/accounts", { limit: 100 })) {
  console.log(row);
}

// Webhook (Standard Webhooks)
await verify(process.env.EB_WEBHOOK_SECRET!, req.headers, rawBody);

// SSE resume
await streamEvents(client, {
  lastEventId: "evt_…",
  onEvent: (ev) => console.log(ev.event, ev.data),
});
```

## Regenerate types

From this directory (needs `api/openapi/openapi.yaml` in the repo):

```bash
npm install
./scripts/generate
# or: npm run generate
```

## Errors

Non-2xx responses with `application/problem+json` throw `ProblemError` with the RFC 9457
fields (`code`, `kind`, `traceId`, …). `429` / `503` are retried (default 3) honouring
`Retry-After`.
