/** Minimal TOTP (SHA1, 30s, 6 digits) for UI login — no extra runtime deps. */
import { createHmac } from "node:crypto";

const base32Decode = (input: string): Buffer => {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const cleaned = input.replace(/=+$/g, "").toUpperCase().replace(/[^A-Z2-7]/g, "");
  let bits = "";
  for (const c of cleaned) {
    const val = alphabet.indexOf(c);
    if (val < 0) continue;
    bits += val.toString(2).padStart(5, "0");
  }
  const bytes: number[] = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) {
    bytes.push(parseInt(bits.slice(i, i + 8), 2));
  }
  return Buffer.from(bytes);
};

export const totpNow = (secret: string, forTime = Date.now()): string => {
  const key = base32Decode(secret);
  const counter = Math.floor(forTime / 1000 / 30);
  const buf = Buffer.alloc(8);
  buf.writeUInt32BE(Math.floor(counter / 0x100000000), 0);
  buf.writeUInt32BE(counter & 0xffffffff, 4);
  const hmac = createHmac("sha1", key).update(buf).digest();
  const offset = hmac[hmac.length - 1]! & 0xf;
  const code =
    ((hmac[offset]! & 0x7f) << 24) |
    ((hmac[offset + 1]! & 0xff) << 16) |
    ((hmac[offset + 2]! & 0xff) << 8) |
    (hmac[offset + 3]! & 0xff);
  return String(code % 1_000_000).padStart(6, "0");
};

/** Codes for now ± skew steps (handles small clock drift / window edges). */
export const totpWindow = (secret: string, skew = 1): string[] => {
  const now = Date.now();
  const out: string[] = [];
  const seen = new Set<string>();
  // Current window first — older codes first can burn a short-lived MFA challenge.
  const steps = [0];
  for (let s = 1; s <= skew; s++) steps.push(-s, s);
  for (const s of steps) {
    const code = totpNow(secret, now + s * 30_000);
    if (!seen.has(code)) {
      seen.add(code);
      out.push(code);
    }
  }
  return out;
};
