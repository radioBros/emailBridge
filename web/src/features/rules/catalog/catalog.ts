/**
 * Event catalogue for the rules editor: the event types and every payload field with its type and
 * description, compiled from `api/events/*.json` by `scripts/gen-rule-catalog.mjs` (checked by the
 * `rules-catalog` test). The JSON lives in the rules chunk, which is only loaded with the editor.
 */

export type FieldType = "string" | "number" | "bool" | "time" | "array" | "object" | "any";

export interface CatalogField {
  path: string;
  type: FieldType;
  description?: string;
  enum?: string[];
  /** Element type of an array. */
  items?: FieldType;
  /** Keys of an array's object elements. */
  itemFields?: string[];
  /** Object with free keys (headers: name → values). */
  map?: boolean;
}

interface CatalogFile {
  types: Record<string, { version: number; fields: CatalogField[] }>;
}

const file = Object.values(
  import.meta.glob("./eventSchemas.json", { eager: true, import: "default" }),
)[0] as CatalogFile;

/** Synthetic types never reach a filter. */
const SYNTHETIC = new Set(["endpoint.test"]);

export const EVENT_TYPES: string[] = Object.keys(file.types)
  .filter((t) => !SYNTHETIC.has(t))
  .sort();

/** Event type → i18n key of its short description. */
export const eventTypeKey = (type: string): string => `rules.event.${type.replace(/\./g, "_")}`;

/** Category of an event type (the part before the first dot). */
export const eventGroupOf = (type: string): string => type.split(".")[0] ?? type;

/** Envelope fields every event carries (CloudEvents), as `event.*`. */
export const ENVELOPE_FIELDS: CatalogField[] = [
  { path: "event.id", type: "string", description: "Event ID (evt_…)" },
  { path: "event.type", type: "string", description: "Event type, also the variable `type`" },
  { path: "event.time", type: "time", description: "When the event happened (RFC 3339)" },
  { path: "event.subject", type: "string", description: "Resource the event is about (msg_…, acc_…)" },
  { path: "event.source", type: "string", description: "Producer of the event" },
  { path: "event.ebaccount", type: "string", description: "Account ID of the event" },
  { path: "event.data", type: "object", description: "The event payload, also the variable `data`" },
];

/** The `account` variable: always strings, "" when unknown. */
export const ACCOUNT_FIELDS: CatalogField[] = [
  { path: "account.id", type: "string", description: "Account ID (acc_…)" },
  { path: "account.externalId", type: "string", description: "Your own ID for the account, \"\" when unset" },
  { path: "account.email", type: "string", description: "Account e-mail address" },
  { path: "account.name", type: "string", description: "Account display name" },
  { path: "account.metadata", type: "object", description: "Your own key/value data on the account (G4); empty map when unset" },
];

/** Payload fields of the given types (all message types when none is given), deduplicated by path. */
export const dataFieldsFor = (types: readonly string[]): CatalogField[] => {
  const chosen = types.length
    ? types.filter((t) => file.types[t])
    : EVENT_TYPES.filter((t) => t.startsWith("message."));
  const byPath = new Map<string, CatalogField>();
  for (const t of chosen) {
    for (const f of file.types[t]?.fields ?? []) {
      if (!byPath.has(f.path)) byPath.set(f.path, f);
    }
  }
  return [...byPath.values()].sort((a, b) => a.path.localeCompare(b.path));
};

/** Every field addressable from a rule for the given types: envelope, account and payload. */
export const allFieldsFor = (types: readonly string[]): CatalogField[] => [
  ...ENVELOPE_FIELDS,
  ...ACCOUNT_FIELDS,
  ...dataFieldsFor(types),
];

/** Direct children of a dotted path (`data.mailbox` → path, specialUse, mailboxId). */
export const childFields = (base: string, types: readonly string[]): CatalogField[] => {
  const prefix = `${base}.`;
  return allFieldsFor(types).filter(
    (f) => f.path.startsWith(prefix) && !f.path.slice(prefix.length).includes("."),
  );
};

export const fieldByPath = (path: string, types: readonly string[]): CatalogField | undefined =>
  allFieldsFor(types).find((f) => f.path === path);
