/**
 * Free-form account metadata (G4): the caller's own small JSON object (CRM IDs, tenant codes, tags).
 * The editor works on rows (key + type + value text); these helpers convert, validate (same limits as the
 * server) and build the JSON Merge Patch sent on PATCH.
 */

/** Local until schema regen (G4). */
export type MetadataValue = string | number | boolean | null;
/** Local until schema regen (G4). */
export type AccountMetadata = Record<string, MetadataValue>;

export const METADATA_TYPES = ["string", "number", "boolean", "null"] as const;
export type MetadataType = (typeof METADATA_TYPES)[number];

export interface MetadataRow {
  /** Stable row id for v-for keys (not sent). */
  id: number;
  key: string;
  type: MetadataType;
  /** Value as typed: text for string/number, "true"/"false" for boolean, ignored for null. */
  value: string;
}

export const METADATA_LIMITS = {
  maxKeys: 32,
  maxKeyLength: 64,
  maxValueBytes: 1024,
  maxBytes: 8 * 1024,
} as const;

const KEY_RE = /^[A-Za-z0-9_.-]{1,64}$/;

let nextId = 1;
const newId = (): number => {
  nextId += 1;
  return nextId;
};

const utf8Bytes = (s: string): number => new TextEncoder().encode(s).length;

export const emptyMetadataRow = (): MetadataRow => ({ id: newId(), key: "", type: "string", value: "" });

const typeOf = (v: MetadataValue): MetadataType => {
  if (v === null) return "null";
  if (typeof v === "number") return "number";
  if (typeof v === "boolean") return "boolean";
  return "string";
};

/** Rows for an account's metadata, sorted by key so the editor is stable. */
export const rowsFromMetadata = (meta: AccountMetadata | null | undefined): MetadataRow[] =>
  Object.keys(meta ?? {})
    .sort((a, b) => a.localeCompare(b))
    .map((key) => {
      const v = (meta as AccountMetadata)[key] ?? null;
      return { id: newId(), key, type: typeOf(v), value: v === null ? "" : String(v) };
    });

/** Value of one row in its JSON type (number rows must hold a finite number — validate first). */
export const rowValue = (row: MetadataRow): MetadataValue => {
  switch (row.type) {
    case "null":
      return null;
    case "number":
      return Number(row.value.trim());
    case "boolean":
      return row.value === "true";
    default:
      return row.value;
  }
};

/** True when the row has nothing typed in it (it is ignored, not an error). */
export const isBlankRow = (row: MetadataRow): boolean =>
  !row.key.trim() && (row.type === "null" || row.type === "boolean" || !row.value);

/** The metadata object the rows describe (blank rows skipped). */
export const metadataFromRows = (rows: readonly MetadataRow[]): AccountMetadata => {
  const out: AccountMetadata = {};
  for (const row of rows) {
    if (isBlankRow(row)) continue;
    out[row.key.trim()] = rowValue(row);
  }
  return out;
};

/** Error codes (i18n keys under `accounts.metadata_err_*`). */
export type MetadataKeyError = "key_required" | "key_invalid" | "key_duplicate";
export type MetadataValueError = "value_number" | "value_too_long";
export type MetadataTotalError = "too_many" | "too_large";

export interface MetadataValidation {
  rows: Record<number, { key?: MetadataKeyError; value?: MetadataValueError }>;
  total?: MetadataTotalError;
  valid: boolean;
}

/** Validates rows with the server's limits: keys, duplicates, value types, key count, serialised size. */
export const validateMetadataRows = (rows: readonly MetadataRow[]): MetadataValidation => {
  const out: MetadataValidation = { rows: {}, valid: true };
  const seen = new Map<string, number>();
  for (const row of rows) {
    if (isBlankRow(row)) continue;
    const errs: { key?: MetadataKeyError; value?: MetadataValueError } = {};
    const key = row.key.trim();
    if (!key) errs.key = "key_required";
    else if (!KEY_RE.test(key)) errs.key = "key_invalid";
    else if (seen.has(key)) errs.key = "key_duplicate";
    if (key) seen.set(key, (seen.get(key) ?? 0) + 1);
    if (row.type === "number") {
      const s = row.value.trim();
      if (!s || !Number.isFinite(Number(s))) errs.value = "value_number";
    }
    if (row.type === "string" && utf8Bytes(row.value) > METADATA_LIMITS.maxValueBytes) {
      errs.value = "value_too_long";
    }
    if (errs.key || errs.value) {
      out.rows[row.id] = errs;
      out.valid = false;
    }
  }
  if (seen.size > METADATA_LIMITS.maxKeys) {
    out.total = "too_many";
    out.valid = false;
  } else if (out.valid && utf8Bytes(JSON.stringify(metadataFromRows(rows))) > METADATA_LIMITS.maxBytes) {
    out.total = "too_large";
    out.valid = false;
  }
  return out;
};

/**
 * JSON Merge Patch (RFC 7396) from `before` to `after`: changed or new keys with their value, removed keys
 * as null. Unchanged keys are not sent. Returns undefined when nothing changed.
 * A stored null value cannot be kept through a patch (null means "remove"), so a key that changes to null
 * is sent as a removal — the server then drops it, which is the documented merge-patch behaviour.
 */
export const metadataMergePatch = (
  before: AccountMetadata | null | undefined,
  after: AccountMetadata,
): AccountMetadata | undefined => {
  const prev = before ?? {};
  const patch: AccountMetadata = {};
  for (const key of Object.keys(prev)) {
    if (!(key in after)) patch[key] = null;
  }
  for (const [key, value] of Object.entries(after)) {
    if (!(key in prev) || prev[key] !== value) patch[key] = value;
  }
  return Object.keys(patch).length ? patch : undefined;
};

/** `body.metadata.<key>` problem paths → message by key (`""` for the whole object). */
export const metadataFieldErrors = (fieldErrors: Record<string, string>): Record<string, string> => {
  const out: Record<string, string> = {};
  for (const [path, message] of Object.entries(fieldErrors)) {
    if (path === "body.metadata") out[""] = message;
    else if (path.startsWith("body.metadata.")) out[path.slice("body.metadata.".length)] = message;
  }
  return out;
};
