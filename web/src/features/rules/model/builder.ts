import type { RuleGroup, RuleLeaf, RuleMatch, RuleNode } from "@/api/rules";
import { fieldByPath, type CatalogField } from "@/features/rules/catalog/catalog";

/**
 * The guided filter builder's model. The tree is exactly the backend builder JSON
 * (`internal/rules/builder.go`), so it is saved as `rule.builder` and compiled by
 * `POST /v1/routes/builder/compile` — the server is the one source of the generated CEL.
 */

export type ValueKind =
  | "none"
  | "text"
  | "textList"
  | "number"
  | "bool"
  | "enum"
  | "eventTypes"
  | "account"
  | "folder"
  | "specialUse"
  | "label"
  | "list"
  | "weekday"
  | "businessHours";

export type FieldGroup =
  | "event"
  | "mailbox"
  | "message"
  | "attachments"
  | "classification"
  | "trust"
  | "time"
  | "payload";

export interface FieldDef {
  /** Builder field id (or a payload path for schema fields). */
  id: string;
  group: FieldGroup;
  ops: string[];
  value: ValueKind;
  /** What `name` holds: a header, a list, or the IANA zone. */
  name?: "header" | "list" | "tz";
  enumValues?: string[];
  /** Value when the leaf is created. */
  initial?: unknown;
}

export const TEXT_OPS = ["eq", "neq", "contains", "startsWith", "endsWith", "matches", "in", "exists", "notExists"];
export const ENUM_OPS = ["eq", "neq", "in"];
export const NUMBER_OPS = ["eq", "neq", "gt", "gte", "lt", "lte"];
export const BOOL_OPS = ["eq"];
export const LIST_OPS = ["includes", "anyOf", "exists", "notExists"];
export const PRESENCE_OPS = ["exists", "notExists"];

export const AUTH_RESULTS = ["pass", "fail", "softfail", "neutral", "none", "temperror", "permerror"];
export const SPECIAL_USES = ["\\Inbox", "\\Sent", "\\Drafts", "\\Junk", "\\Trash", "\\Archive", "\\All", "\\Flagged"];
export const PEC_TYPES = [
  "posta-certificata",
  "accettazione",
  "non-accettazione",
  "presa-in-carico",
  "avvenuta-consegna",
  "errore-consegna",
  "preavviso-errore-consegna",
  "rilevazione-virus",
];
export const FLAGS = ["\\Seen", "\\Flagged", "\\Answered", "\\Draft", "\\Deleted", "$Forwarded", "$Junk"];

/** The fixed fields, in the order the picker shows them. */
export const FIELD_DEFS: FieldDef[] = [
  { id: "type", group: "event", ops: ["in", "eq", "neq"], value: "eventTypes", initial: ["message.received"] },
  { id: "account.id", group: "event", ops: ["eq", "neq", "in"], value: "account" },
  { id: "account.externalId", group: "event", ops: TEXT_OPS, value: "text" },
  { id: "folder", group: "mailbox", ops: ["eq", "neq", "startsWith", "contains", "in"], value: "folder", initial: "INBOX" },
  { id: "isInbox", group: "mailbox", ops: BOOL_OPS, value: "bool", initial: true },
  { id: "mailbox.specialUse", group: "mailbox", ops: ENUM_OPS, value: "specialUse", enumValues: SPECIAL_USES, initial: "\\Inbox" },
  { id: "label", group: "mailbox", ops: ["eq"], value: "label" },
  { id: "flags", group: "mailbox", ops: ["contains", "in", "exists", "notExists"], value: "enum", enumValues: FLAGS, initial: "\\Flagged" },
  { id: "subject", group: "message", ops: TEXT_OPS, value: "text" },
  { id: "from.address", group: "message", ops: TEXT_OPS, value: "text" },
  { id: "from.domain", group: "message", ops: TEXT_OPS, value: "text" },
  { id: "to.domain", group: "message", ops: TEXT_OPS, value: "text" },
  { id: "inList", group: "message", ops: ["eq"], value: "list", name: "list" },
  { id: "header", group: "message", ops: TEXT_OPS, value: "text", name: "header" },
  { id: "body", group: "message", ops: ["contains", "matches", "exists", "notExists"], value: "text" },
  { id: "size", group: "message", ops: NUMBER_OPS, value: "number", initial: 1048576 },
  { id: "hasAttachments", group: "attachments", ops: BOOL_OPS, value: "bool", initial: true },
  { id: "attachment.name", group: "attachments", ops: ["eq"], value: "text", initial: "*.pdf" },
  { id: "attachment.type", group: "attachments", ops: ["eq"], value: "text", initial: "application/pdf" },
  { id: "attachment.size", group: "attachments", ops: NUMBER_OPS, value: "number", initial: 1048576 },
  { id: "attachments", group: "attachments", ops: ["eq", "neq", "exists", "notExists"], value: "number", initial: 1 },
  { id: "isReply", group: "classification", ops: BOOL_OPS, value: "bool", initial: true },
  { id: "isForward", group: "classification", ops: BOOL_OPS, value: "bool", initial: true },
  { id: "isAutoReply", group: "classification", ops: BOOL_OPS, value: "bool", initial: false },
  { id: "isBounce", group: "classification", ops: BOOL_OPS, value: "bool", initial: true },
  { id: "auth.spf", group: "trust", ops: ENUM_OPS, value: "enum", enumValues: AUTH_RESULTS, initial: "pass" },
  { id: "auth.dkim", group: "trust", ops: ENUM_OPS, value: "enum", enumValues: AUTH_RESULTS, initial: "pass" },
  { id: "auth.dmarc", group: "trust", ops: ENUM_OPS, value: "enum", enumValues: AUTH_RESULTS, initial: "pass" },
  { id: "spamScore", group: "trust", ops: NUMBER_OPS, value: "number", initial: 5 },
  { id: "pecType", group: "trust", ops: ENUM_OPS, value: "enum", enumValues: PEC_TYPES, initial: "avvenuta-consegna" },
  { id: "businessHours", group: "time", ops: ["eq"], value: "businessHours" },
  { id: "weekday", group: "time", ops: NUMBER_OPS, value: "weekday", name: "tz", initial: 6 },
  { id: "hour", group: "time", ops: NUMBER_OPS, value: "number", name: "tz", initial: 9 },
];

const byId = new Map(FIELD_DEFS.map((d) => [d.id, d]));

export const isPayloadPath = (field: string): boolean =>
  /^(data|event|account)(\.[A-Za-z_][A-Za-z0-9_]{0,63}){1,8}$/.test(field) && !byId.has(field);

/** The definition of a schema field picked from the payload tree. */
export const payloadFieldDef = (f: CatalogField): FieldDef => {
  if (f.enum?.length) return { id: f.path, group: "payload", ops: ENUM_OPS, value: "enum", enumValues: f.enum };
  switch (f.type) {
    case "number":
      return { id: f.path, group: "payload", ops: NUMBER_OPS, value: "number", initial: 0 };
    case "bool":
      return { id: f.path, group: "payload", ops: BOOL_OPS, value: "bool", initial: true };
    case "array":
      return f.items === "string"
        ? { id: f.path, group: "payload", ops: LIST_OPS, value: "textList" }
        : { id: f.path, group: "payload", ops: PRESENCE_OPS, value: "none" };
    case "object":
    case "any":
      return { id: f.path, group: "payload", ops: PRESENCE_OPS, value: "none" };
    default:
      return { id: f.path, group: "payload", ops: TEXT_OPS, value: "text" };
  }
};

export const defOf = (field: string, types: readonly string[] = []): FieldDef => {
  const fixed = byId.get(field);
  if (fixed) return fixed;
  const schema = fieldByPath(field, types);
  if (schema) return payloadFieldDef(schema);
  return { id: field, group: "payload", ops: TEXT_OPS, value: "text" };
};

export const isGroup = (n: RuleNode): n is RuleGroup =>
  Array.isArray((n as RuleGroup).conditions) || ("match" in n && !("field" in n));

/** Ops whose value input is hidden. */
export const opNeedsValue = (op: string): boolean => op !== "exists" && op !== "notExists";

/** Ops that take a list of values. */
export const opTakesList = (op: string): boolean => op === "in" || op === "anyOf";

export const defaultTz = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
};

/** A fresh leaf for a field, with its first op and a sensible value. */
export const newLeaf = (field: string, types: readonly string[] = []): RuleLeaf => {
  const def = defOf(field, types);
  const op = def.ops[0] ?? "eq";
  const leaf: RuleLeaf = { field, op };
  if (def.name === "tz") leaf.name = defaultTz();
  if (def.value === "businessHours") {
    leaf.value = { tz: defaultTz(), window: "09:00-18:00", days: "Mon-Fri" };
  } else if (def.initial !== undefined) {
    leaf.value = Array.isArray(def.initial) ? [...def.initial] : def.initial;
  } else if (opNeedsValue(op)) {
    leaf.value = opTakesList(op) ? [] : "";
  }
  return leaf;
};

export const newGroup = (match: RuleMatch = "all", conditions: RuleNode[] = []): RuleGroup => ({
  match,
  conditions,
});

/** Value after an op change: lists ↔ scalars convert, presence ops drop the value. */
export const valueForOp = (leaf: RuleLeaf, op: string): unknown => {
  if (!opNeedsValue(op)) return undefined;
  const v = leaf.value;
  if (opTakesList(op)) {
    if (Array.isArray(v)) return v;
    return v === undefined || v === "" || v === null ? [] : [v];
  }
  if (Array.isArray(v)) return v[0] ?? "";
  return v ?? "";
};

/** Depth of the deepest group (root = 1); the backend allows 8 nested levels. */
export const depthOf = (g: RuleGroup): number =>
  1 + Math.max(0, ...g.conditions.filter(isGroup).map((c) => depthOf(c as RuleGroup)));

export const MAX_DEPTH = 8;

/**
 * Prepares the tree for the compile API: drops incomplete leaves (no field / empty required value),
 * turns a single-value `type in [x]` into `type == x`, and removes empty groups. Pure; the editor's
 * tree is left alone.
 */
export const normalizeForCompile = (g: RuleGroup): RuleGroup => {
  const conditions: RuleNode[] = [];
  for (const c of g.conditions) {
    if (isGroup(c)) {
      const inner = normalizeForCompile(c);
      if (inner.conditions.length) conditions.push(inner);
      continue;
    }
    if (!isComplete(c)) continue;
    if (c.field === "type" && c.op === "in" && Array.isArray(c.value) && c.value.length === 1) {
      conditions.push({ field: "type", op: "eq", value: c.value[0] });
      continue;
    }
    conditions.push({ ...c });
  }
  return { match: g.match, conditions };
};

export const isComplete = (c: RuleLeaf): boolean => {
  if (!c.field || !c.op) return false;
  const def = defOf(c.field);
  if (def.name === "header" && !c.name?.trim()) return false;
  if (def.name === "list" && !c.name?.trim()) return false;
  if (!opNeedsValue(c.op) || def.value === "none" || def.value === "list") return true;
  if (def.value === "label" || def.value === "text" || def.value === "folder" || def.value === "account") {
    if (Array.isArray(c.value)) return c.value.length > 0;
    return typeof c.value === "string" ? c.value.trim() !== "" : c.value != null;
  }
  if (Array.isArray(c.value)) return c.value.length > 0;
  return c.value !== undefined && c.value !== null && c.value !== "";
};

/** Every account ID the tree references (for the folder / label pickers). */
export const accountIdsIn = (g: RuleGroup): string[] => {
  const out: string[] = [];
  for (const c of g.conditions) {
    if (isGroup(c)) out.push(...accountIdsIn(c));
    else if (c.field === "account.id" && c.op !== "neq") {
      for (const v of Array.isArray(c.value) ? c.value : [c.value]) {
        if (typeof v === "string" && v) out.push(v);
      }
    }
  }
  return [...new Set(out)];
};

/** Every event type the tree allows (empty = not restricted). */
export const eventTypesIn = (g: RuleGroup): string[] => {
  const out: string[] = [];
  for (const c of g.conditions) {
    if (isGroup(c)) {
      if (c.match !== "not") out.push(...eventTypesIn(c));
    } else if (c.field === "type" && (c.op === "in" || c.op === "eq")) {
      for (const v of Array.isArray(c.value) ? c.value : [c.value]) {
        if (typeof v === "string" && v) out.push(v);
      }
    }
  }
  return [...new Set(out)];
};

/** Deep copy (the builder state is plain JSON). */
export const cloneTree = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

/** The virtual folder choice that means "Inbox on any provider" (`isInbox()`). */
export const VIRTUAL_INBOX = "__inbox__";

/** A folder leaf showing the virtual Inbox becomes `isInbox` (eq → true, neq → false) and back. */
export const folderLeafFor = (op: string, value: unknown): RuleLeaf => {
  if (value === VIRTUAL_INBOX) {
    if (op === "eq" || op === "neq") return { field: "isInbox", op: "eq", value: op === "eq" };
    return { field: "folder", op, value: "INBOX" };
  }
  return { field: "folder", op, value };
};

/** How an `isInbox` leaf shows in the folder row: op + the virtual choice. */
export const folderViewOf = (leaf: RuleLeaf): { op: string; value: unknown } =>
  leaf.field === "isInbox"
    ? { op: leaf.value === false ? "neq" : "eq", value: VIRTUAL_INBOX }
    : { op: leaf.op, value: leaf.value };

/** Stable keys for v-for over a tree mutated in place. */
const keys = new WeakMap<object, string>();
let seq = 0;
export const keyOf = (node: object): string => {
  let k = keys.get(node);
  if (!k) {
    seq += 1;
    k = `n${seq}`;
    keys.set(node, k);
  }
  return k;
};
