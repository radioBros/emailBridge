/**
 * Minimal RFC 822 / MIME parser for sandbox display only.
 * Handles single-part and common multipart (alternative / mixed / related).
 * Not a full MIME library — nested edge cases fall back to raw text.
 */

export interface MimeHeader {
  name: string;
  value: string;
}

export interface MimeAttachment {
  filename: string;
  contentType: string;
  size: number;
  contentId?: string;
}

export interface MimeParseResult {
  headers: MimeHeader[];
  text?: string;
  html?: string;
  attachments: MimeAttachment[];
  /** True when structure was only partially understood. */
  partial: boolean;
}

const HEADER_LINE = /^([\w-]+):\s*(.*)$/i;

const decodeQuotedPrintable = (input: string): string => {
  const soft = input.replace(/=\r?\n/g, "");
  return soft.replace(/=([0-9A-Fa-f]{2})/g, (_, hex: string) =>
    String.fromCharCode(parseInt(hex, 16)),
  );
};

const decodeBody = (body: string, encoding: string): string => {
  const enc = encoding.toLowerCase().trim();
  if (enc === "base64") {
    try {
      const cleaned = body.replace(/\s+/g, "");
      return atob(cleaned);
    } catch {
      return body;
    }
  }
  if (enc === "quoted-printable") return decodeQuotedPrintable(body);
  return body;
};

const parseHeaders = (block: string): { headers: MimeHeader[]; map: Record<string, string> } => {
  const headers: MimeHeader[] = [];
  const map: Record<string, string> = {};
  const folded = block.replace(/\r?\n[ \t]+/g, " ");
  for (const line of folded.split(/\r?\n/)) {
    const m = HEADER_LINE.exec(line);
    if (!m) continue;
    const name = m[1]!;
    const value = m[2]!.trim();
    headers.push({ name, value });
    map[name.toLowerCase()] = value;
  }
  return { headers, map };
};

const contentTypeOf = (map: Record<string, string>): { type: string; params: Record<string, string> } => {
  const raw = map["content-type"] ?? "text/plain";
  const parts = raw.split(";").map((p) => p.trim());
  const type = (parts[0] ?? "text/plain").toLowerCase();
  const params: Record<string, string> = {};
  for (const p of parts.slice(1)) {
    const eq = p.indexOf("=");
    if (eq < 0) continue;
    const k = p.slice(0, eq).trim().toLowerCase();
    let v = p.slice(eq + 1).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    params[k] = v;
  }
  return { type, params };
};

const filenameOf = (map: Record<string, string>, params: Record<string, string>): string => {
  const disp = map["content-disposition"] ?? "";
  const m = /filename\*?=(?:UTF-8''|")?([^";]+)"?/i.exec(disp);
  if (m?.[1]) return decodeURIComponent(m[1].replace(/"/g, ""));
  if (params.name) return params.name;
  return "attachment";
};

const splitMultipart = (body: string, boundary: string): string[] => {
  const delim = `--${boundary}`;
  const parts = body.split(delim);
  const out: string[] = [];
  for (const part of parts) {
    const trimmed = part.replace(/^\r?\n/, "").replace(/--\r?\n?$/, "").trim();
    if (!trimmed || trimmed === "--") continue;
    out.push(trimmed.replace(/\r?\n--$/, ""));
  }
  return out;
};

const parsePart = (
  raw: string,
  result: MimeParseResult,
  depth: number,
): void => {
  if (depth > 6) {
    result.partial = true;
    return;
  }
  const sep = raw.search(/\r?\n\r?\n/);
  const head = sep < 0 ? raw : raw.slice(0, sep);
  const body = sep < 0 ? "" : raw.slice(sep).replace(/^\r?\n\r?\n/, "");
  const { map } = parseHeaders(head);
  const { type, params } = contentTypeOf(map);
  const encoding = map["content-transfer-encoding"] ?? "7bit";
  const disposition = (map["content-disposition"] ?? "").toLowerCase();

  if (type.startsWith("multipart/") && params.boundary) {
    for (const part of splitMultipart(body, params.boundary)) {
      parsePart(part, result, depth + 1);
    }
    return;
  }

  const isAttachment =
    disposition.includes("attachment") ||
    (disposition.includes("inline") && Boolean(map["content-id"]) && !type.startsWith("text/"));

  if (isAttachment || (!type.startsWith("text/") && type !== "message/rfc822")) {
    const decoded = decodeBody(body, encoding);
    result.attachments.push({
      filename: filenameOf(map, params),
      contentType: type,
      size: decoded.length,
      contentId: map["content-id"]?.replace(/^<|>$/g, ""),
    });
    return;
  }

  const decoded = decodeBody(body, encoding);
  if (type.startsWith("text/html")) {
    if (!result.html) result.html = decoded;
  } else if (type.startsWith("text/plain")) {
    if (!result.text) result.text = decoded;
  } else {
    result.partial = true;
  }
};

/** Parse a raw RFC 822 message (UTF-8 string) for UI display. */
export const parseMime = (raw: string): MimeParseResult => {
  const result: MimeParseResult = {
    headers: [],
    attachments: [],
    partial: false,
  };
  const sep = raw.search(/\r?\n\r?\n/);
  const head = sep < 0 ? raw : raw.slice(0, sep);
  const body = sep < 0 ? "" : raw.slice(sep).replace(/^\r?\n\r?\n/, "");
  const { headers, map } = parseHeaders(head);
  result.headers = headers;

  const { type, params } = contentTypeOf(map);
  const encoding = map["content-transfer-encoding"] ?? "7bit";

  if (type.startsWith("multipart/") && params.boundary) {
    for (const part of splitMultipart(body, params.boundary)) {
      parsePart(part, result, 0);
    }
    if (!result.text && !result.html) result.partial = true;
    return result;
  }

  const decoded = decodeBody(body, encoding);
  if (type.startsWith("text/html")) result.html = decoded;
  else result.text = decoded;
  return result;
};

/** Decode ArrayBuffer as UTF-8 text (lossy for binary-only parts). */
export const bytesToUtf8 = (bytes: ArrayBuffer): string => {
  try {
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
  } catch {
    return "";
  }
};

/** Strip script tags as defence-in-depth before sandbox iframe srcdoc. */
export const stripScripts = (html: string): string =>
  html
    .replace(/<script\b[^<]*(?:(?!<\/script>)<[^<]*)*<\/script>/gi, "")
    .replace(/\son\w+\s*=\s*(['"]).*?\1/gi, "")
    .replace(/\son\w+\s*=\s*[^\s>]+/gi, "");

/** Build srcdoc with CSP that blocks or allows remote images. */
export const buildHtmlSrcdoc = (html: string, loadImages: boolean): string => {
  const safe = stripScripts(html);
  const imgSrc = loadImages ? "img-src * data: blob:;" : "img-src 'none' data: blob:;";
  const csp = `default-src 'none'; style-src 'unsafe-inline'; ${imgSrc}`;
  return `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="${csp}"></head><body>${safe}</body></html>`;
};
