/** Tags / attrs stripped before send (backend expects plain HTML, no script). */
const BLOCKED_TAGS = /<\/?(?:script|style|iframe|object|embed|form|link|meta|base)[^>]*>/gi;
const EVENT_ATTRS = /\s+on[a-z]+\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+)/gi;
const JS_HREF = /\s+(href|src)\s*=\s*(?:"\s*javascript:[^"]*"|'\s*javascript:[^']*'|\s*javascript:[^\s>]+)/gi;

/** Strip dangerous tags/attrs; keep formatting the editor produces. */
export const sanitizeHtml = (html: string): string => {
  if (!html) return "";
  return html
    .replace(BLOCKED_TAGS, "")
    .replace(EVENT_ATTRS, "")
    .replace(JS_HREF, "")
    .trim();
};

const BLOCK_CLOSE = /<\/(p|div|h[1-6]|li|tr|blockquote|pre|br)\s*>/gi;
const BR = /<br\s*\/?>/gi;
const TAGS = /<[^>]+>/g;

/** Decode common HTML entities without re-parsing as markup. */
const decodeEntities = (s: string): string =>
  s
    .replace(/&nbsp;/gi, " ")
    .replace(/&amp;/gi, "&")
    .replace(/&lt;/gi, "<")
    .replace(/&gt;/gi, ">")
    .replace(/&quot;/gi, '"')
    .replace(/&#39;|&apos;/gi, "'")
    .replace(/&#(\d+);/g, (_, n: string) => String.fromCharCode(Number(n)))
    .replace(/&#x([0-9a-f]+);/gi, (_, h: string) => String.fromCharCode(parseInt(h, 16)));

/** Plain-text body derived from HTML for the multipart/alternative part. */
export const htmlToText = (html: string): string => {
  if (!html) return "";
  const s = decodeEntities(
    html.replace(BR, "\n").replace(BLOCK_CLOSE, "\n").replace(TAGS, ""),
  );
  return s
    .replace(/\u00a0/g, " ")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
};

/** True when the HTML has no visible text (empty paragraph / whitespace). */
export const isEmptyHtml = (html: string): boolean => htmlToText(html).length === 0;
