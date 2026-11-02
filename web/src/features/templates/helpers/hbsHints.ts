/** Extract Handlebars path names used as variables (not helpers/partials). */
const VAR_RE = /\{\{\{?\s*([a-zA-Z_][\w.]*)\s*(?:\}\}\}?)/g;
const HELPER_OR_PARTIAL = /^(?:else|if|unless|each|with|lookup|log|>|#|\/)/;

export const extractHbsVariables = (...sources: (string | undefined | null)[]): string[] => {
  const found = new Set<string>();
  for (const src of sources) {
    if (!src) continue;
    VAR_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = VAR_RE.exec(src)) !== null) {
      const raw = m[1] ?? "";
      if (!raw || HELPER_OR_PARTIAL.test(raw) || raw.startsWith("@")) continue;
      const root = raw.split(".")[0] ?? raw;
      if (root) found.add(root);
    }
  }
  return [...found].sort((a, b) => a.localeCompare(b));
};

export const parseSampleVariables = (
  raw: string,
): { ok: true; value: Record<string, unknown> } | { ok: false; error: string } => {
  const trimmed = raw.trim();
  if (!trimmed) return { ok: true, value: {} };
  try {
    const parsed = JSON.parse(trimmed) as unknown;
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { ok: false, error: "object" };
    }
    return { ok: true, value: parsed as Record<string, unknown> };
  } catch {
    return { ok: false, error: "json" };
  }
};

export const DEFAULT_SAMPLE_VARS = `{
  "name": "Ada",
  "email": "ada@example.test"
}`;
