import { describe, expect, it } from "vitest";
import {
  buildHtmlSrcdoc,
  bytesToUtf8,
  parseMime,
  stripScripts,
} from "@/features/sandbox/helpers/mimeParse";
import { formatRecipients, toApiDate } from "@/features/sandbox/helpers/format";

describe("mimeParse", () => {
  it("parses simple text/plain", () => {
    const raw = [
      "From: a@test",
      "To: b@test",
      "Subject: Hi",
      "Content-Type: text/plain; charset=utf-8",
      "",
      "Hello world",
    ].join("\r\n");
    const parsed = parseMime(raw);
    expect(parsed.text).toContain("Hello world");
    expect(parsed.headers.some((h) => h.name === "Subject")).toBe(true);
    expect(parsed.partial).toBe(false);
  });

  it("parses multipart alternative", () => {
    const raw = [
      "Content-Type: multipart/alternative; boundary=bound",
      "",
      "--bound",
      "Content-Type: text/plain",
      "",
      "plain part",
      "--bound",
      "Content-Type: text/html",
      "",
      "<p>html part</p>",
      "--bound--",
    ].join("\r\n");
    const parsed = parseMime(raw);
    expect(parsed.text).toContain("plain part");
    expect(parsed.html).toContain("html part");
  });

  it("lists attachments", () => {
    const raw = [
      "Content-Type: multipart/mixed; boundary=b",
      "",
      "--b",
      "Content-Type: text/plain",
      "",
      "body",
      "--b",
      'Content-Type: application/pdf',
      'Content-Disposition: attachment; filename="doc.pdf"',
      "Content-Transfer-Encoding: base64",
      "",
      "AQID",
      "--b--",
    ].join("\r\n");
    const parsed = parseMime(raw);
    expect(parsed.attachments).toHaveLength(1);
    expect(parsed.attachments[0]!.filename).toBe("doc.pdf");
  });

  it("builds srcdoc with image CSP toggle", () => {
    const blocked = buildHtmlSrcdoc("<p>x</p><script>alert(1)</script>", false);
    expect(blocked).toContain("img-src 'none'");
    expect(blocked).not.toContain("<script>");
    const allowed = buildHtmlSrcdoc("<p>x</p>", true);
    expect(allowed).toContain("img-src *");
  });

  it("strips inline handlers", () => {
    expect(stripScripts('<img src=x onerror="alert(1)">')).not.toContain("onerror");
  });

  it("decodes utf-8 bytes", () => {
    const buf = new TextEncoder().encode("café").buffer;
    expect(bytesToUtf8(buf)).toContain("caf");
  });

  it("decodes quoted-printable html", () => {
    const raw = [
      "Content-Type: text/html; charset=utf-8",
      "Content-Transfer-Encoding: quoted-printable",
      "",
      "<p>Hello=20world</p>",
    ].join("\r\n");
    expect(parseMime(raw).html).toContain("Hello world");
  });
});

describe("sandbox format helpers", () => {
  it("formats recipients and dates", () => {
    expect(formatRecipients(["a@x", "b@x"])).toBe("a@x +1");
    expect(toApiDate("")).toBeUndefined();
    expect(toApiDate("2026-01-01T00:00")).toMatch(/^\d{4}-\d{2}-\d{2}T/);
  });
});
