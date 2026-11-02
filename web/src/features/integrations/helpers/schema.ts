/** JSON Schema helpers for the install dialog (P8.1 subset). */

export interface SchemaProperty {
  key: string;
  type: string;
  title: string;
  description?: string;
  format?: string;
  writeOnly?: boolean;
  required: boolean;
  default?: unknown;
  minLength?: number;
  maxLength?: number;
}

export const schemaProperties = (
  schema: Record<string, unknown> | null | undefined,
  secretFields: string[] = [],
): SchemaProperty[] => {
  if (!schema) return [];
  const props = (schema.properties ?? {}) as Record<string, Record<string, unknown>>;
  const required = new Set(
    Array.isArray(schema.required) ? (schema.required as string[]) : [],
  );
  const secrets = new Set(secretFields);
  return Object.entries(props).map(([key, raw]) => {
    const writeOnly = Boolean(raw.writeOnly) || secrets.has(key);
    return {
      key,
      type: String(raw.type ?? "string"),
      title: String(raw.title ?? key),
      description: raw.description ? String(raw.description) : undefined,
      format: raw.format ? String(raw.format) : undefined,
      writeOnly,
      required: required.has(key),
      default: raw.default,
      minLength: typeof raw.minLength === "number" ? raw.minLength : undefined,
      maxLength: typeof raw.maxLength === "number" ? raw.maxLength : undefined,
    };
  });
};

export const defaultsFromSchema = (
  schema: Record<string, unknown> | null | undefined,
): Record<string, unknown> => {
  const out: Record<string, unknown> = {};
  for (const p of schemaProperties(schema)) {
    if (p.writeOnly) continue;
    if (p.default !== undefined) out[p.key] = p.default;
  }
  return out;
};

export const categoryOrder = [
  "chat",
  "helpdesk",
  "automation",
  "sending",
  "spam",
  "storage",
  "crm",
  "sso",
  "observability",
] as const;

export const groupByCategory = <T extends { category: string }>(
  items: T[],
): { category: string; items: T[] }[] => {
  const map = new Map<string, T[]>();
  for (const it of items) {
    const list = map.get(it.category) ?? [];
    list.push(it);
    map.set(it.category, list);
  }
  const ordered: { category: string; items: T[] }[] = [];
  for (const c of categoryOrder) {
    const itemsFor = map.get(c);
    if (itemsFor?.length) ordered.push({ category: c, items: itemsFor });
    map.delete(c);
  }
  for (const [category, itemsFor] of map) {
    ordered.push({ category, items: itemsFor });
  }
  return ordered;
};
