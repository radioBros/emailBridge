/** Standard Webhooks + EmailBridge bodyOnly verification (S7 §2.2–2.3). */

export const HEADER_WEBHOOK_ID = "webhook-id";
export const HEADER_WEBHOOK_TIMESTAMP = "webhook-timestamp";
export const HEADER_WEBHOOK_SIGNATURE = "webhook-signature";
export const HEADER_EB_SIGNATURE = "eb-signature";

export const DEFAULT_TOLERANCE_MS = 5 * 60 * 1000;

export class WebhookError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "WebhookError";
  }
}

const enc = new TextEncoder();

const b64ToBytes = (b64: string): Uint8Array => {
  if (typeof Buffer !== "undefined") {
    return new Uint8Array(Buffer.from(b64, "base64"));
  }
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
};

const bytesToB64 = (bytes: ArrayBuffer | Uint8Array): string => {
  const u8 = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  if (typeof Buffer !== "undefined") {
    return Buffer.from(u8).toString("base64");
  }
  let s = "";
  for (const b of u8) s += String.fromCharCode(b);
  return btoa(s);
};

const timingSafeEqual = (a: string, b: string): boolean => {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
};

const decodeSecret = (secret: string): Uint8Array => {
  const raw = secret.startsWith("whsec_") ? secret.slice(6) : secret;
  const key = b64ToBytes(raw);
  if (key.length === 0) throw new WebhookError("webhook: empty secret");
  return key;
};

const asBufferSource = (u8: Uint8Array): ArrayBuffer => {
  const copy = new Uint8Array(u8.byteLength);
  copy.set(u8);
  return copy.buffer;
};

const hmacSha256B64 = async (key: Uint8Array, data: Uint8Array): Promise<string> => {
  const cryptoKey = await crypto.subtle.importKey(
    "raw",
    asBufferSource(key),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const sig = await crypto.subtle.sign("HMAC", cryptoKey, asBufferSource(data));
  return bytesToB64(sig);
};

const bodyBytes = (body: string | Uint8Array): Uint8Array =>
  typeof body === "string" ? enc.encode(body) : body;

const headerGet = (headers: Headers | Record<string, string>, name: string): string => {
  if (headers instanceof Headers) return headers.get(name) ?? "";
  const lower = name.toLowerCase();
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === lower) return v;
  }
  return "";
};

const matchSignatures = (header: string, expectedB64: string): void => {
  for (const part of header.split(" ")) {
    const p = part.trim();
    if (!p) continue;
    const i = p.indexOf(",");
    if (i < 0) continue;
    const version = p.slice(0, i);
    const sig = p.slice(i + 1);
    if (version === "v1" && timingSafeEqual(sig, expectedB64)) return;
  }
  throw new WebhookError("webhook: signature mismatch");
};

/** Sign Standard Webhooks payload; returns `v1,<base64>`. */
export const sign = async (
  secret: string,
  id: string,
  tsUnix: number,
  body: string | Uint8Array,
): Promise<string> => {
  const key = decodeSecret(secret);
  const msg = new Uint8Array([...enc.encode(`${id}.${tsUnix}.`), ...bodyBytes(body)]);
  const sig = await hmacSha256B64(key, msg);
  return `v1,${sig}`;
};

/** bodyOnly: `v1,<base64(HMAC-SHA256(rawSecret, body))>`. */
export const signBodyOnly = async (secret: string, body: string | Uint8Array): Promise<string> => {
  const sig = await hmacSha256B64(enc.encode(secret), bodyBytes(body));
  return `v1,${sig}`;
};

export type VerifyOptions = {
  now?: number; // unix ms
  toleranceMs?: number;
};

/** Verify Standard Webhooks headers against body. */
export const verify = async (
  secret: string,
  headers: Headers | Record<string, string>,
  body: string | Uint8Array,
  opts: VerifyOptions = {},
): Promise<void> => {
  const id = headerGet(headers, HEADER_WEBHOOK_ID);
  const tsHdr = headerGet(headers, HEADER_WEBHOOK_TIMESTAMP);
  const sigHdr = headerGet(headers, HEADER_WEBHOOK_SIGNATURE);
  if (!id || !tsHdr || !sigHdr) throw new WebhookError("webhook: missing headers");
  const tsUnix = Number(tsHdr);
  if (!Number.isFinite(tsUnix)) throw new WebhookError("webhook: timestamp out of tolerance");
  const now = opts.now ?? Date.now();
  const tolerance = opts.toleranceMs ?? DEFAULT_TOLERANCE_MS;
  if (Math.abs(now - tsUnix * 1000) > tolerance) {
    throw new WebhookError("webhook: timestamp out of tolerance");
  }
  const key = decodeSecret(secret);
  const msg = new Uint8Array([...enc.encode(`${id}.${tsUnix}.`), ...bodyBytes(body)]);
  const expected = await hmacSha256B64(key, msg);
  matchSignatures(sigHdr, expected);
};

/** Verify bodyOnly `eb-signature` (raw secret, not whsec_-decoded). */
export const verifyBodyOnly = async (
  secret: string,
  headers: Headers | Record<string, string>,
  body: string | Uint8Array,
): Promise<void> => {
  const sigHdr = headerGet(headers, HEADER_EB_SIGNATURE);
  if (!sigHdr) throw new WebhookError("webhook: missing headers");
  const expected = await hmacSha256B64(enc.encode(secret), bodyBytes(body));
  matchSignatures(sigHdr, expected);
};
