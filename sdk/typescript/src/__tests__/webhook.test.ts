import { describe, expect, it } from "vitest";
import {
  sign,
  signBodyOnly,
  verify,
  verifyBodyOnly,
  WebhookError,
  HEADER_WEBHOOK_ID,
  HEADER_WEBHOOK_TIMESTAMP,
  HEADER_WEBHOOK_SIGNATURE,
  HEADER_EB_SIGNATURE,
} from "../webhook.js";

const key = Buffer.from("0123456789abcdef0123456789abcdef");
const secret = "whsec_" + key.toString("base64");
const body = '{"ok":true}';
const id = "evt_1";
const ts = 1700000000;

describe("webhook", () => {
  it("signs and verifies Standard Webhooks", async () => {
    const sig = await sign(secret, id, ts, body);
    const headers = {
      [HEADER_WEBHOOK_ID]: id,
      [HEADER_WEBHOOK_TIMESTAMP]: String(ts),
      [HEADER_WEBHOOK_SIGNATURE]: sig,
    };
    await verify(secret, headers, body, { now: ts * 1000 });
  });

  it("rejects bad signature", async () => {
    const headers = {
      [HEADER_WEBHOOK_ID]: id,
      [HEADER_WEBHOOK_TIMESTAMP]: String(ts),
      [HEADER_WEBHOOK_SIGNATURE]: "v1,aaaa",
    };
    await expect(verify(secret, headers, body, { now: ts * 1000 })).rejects.toBeInstanceOf(WebhookError);
  });

  it("rejects stale timestamp", async () => {
    const sig = await sign(secret, id, ts, body);
    const headers = {
      [HEADER_WEBHOOK_ID]: id,
      [HEADER_WEBHOOK_TIMESTAMP]: String(ts),
      [HEADER_WEBHOOK_SIGNATURE]: sig,
    };
    await expect(verify(secret, headers, body, { now: (ts + 600) * 1000 })).rejects.toBeInstanceOf(
      WebhookError,
    );
  });

  it("verifies bodyOnly", async () => {
    const raw = "org-secret";
    const sig = await signBodyOnly(raw, body);
    await verifyBodyOnly(raw, { [HEADER_EB_SIGNATURE]: sig }, body);
  });

  it("accepts dual signatures", async () => {
    const sig = await sign(secret, id, ts, body);
    const headers = {
      [HEADER_WEBHOOK_ID]: id,
      [HEADER_WEBHOOK_TIMESTAMP]: String(ts),
      [HEADER_WEBHOOK_SIGNATURE]: `v1,deadbeef ${sig}`,
    };
    await verify(secret, headers, body, { now: ts * 1000 });
  });
});
