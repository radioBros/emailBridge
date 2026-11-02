// W73b: compiles the event JSON Schemas (`api/events/*.v1.json`) into the compact field catalogue the rules
// editor uses for its field picker, map editor and code autocomplete. The web build never reads the repo's
// api/ folder at run time: this script writes src/features/rules/catalog/eventSchemas.json, and the
// `rules-catalog` test fails when that file drifts from the schemas.
//
//   node scripts/gen-rule-catalog.mjs          write the catalogue
//   node scripts/gen-rule-catalog.mjs --check  exit 1 when it is out of date
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const SCHEMAS = join(here, "..", "..", "api", "events");
const OUT = join(here, "..", "src", "features", "rules", "catalog", "eventSchemas.json");
const MAX_DEPTH = 4;

const typeOf = (schema) => {
  const raw = Array.isArray(schema.type) ? schema.type.filter((t) => t !== "null") : [schema.type];
  const t = raw[0];
  if (t === "integer" || t === "number") return "number";
  if (t === "string" && schema.format === "date-time") return "time";
  if (t === "string" || t === "boolean" || t === "array") return t === "boolean" ? "bool" : t;
  if (t === "object" || schema.properties || schema.additionalProperties) return "object";
  return "any";
};

const resolve = (schema, defs) => {
  if (schema && schema.$ref) {
    const name = schema.$ref.replace("#/$defs/", "");
    const target = defs[name] ?? {};
    return { ...target, description: schema.description ?? target.description };
  }
  return schema ?? {};
};

const walk = (props, defs, prefix, depth, out) => {
  for (const [key, raw] of Object.entries(props).sort(([a], [b]) => a.localeCompare(b))) {
    const schema = resolve(raw, defs);
    const path = `${prefix}.${key}`;
    const type = typeOf(schema);
    const field = { path, type };
    if (schema.description) field.description = schema.description;
    if (schema.enum) field.enum = schema.enum;
    if (type === "array") {
      const items = resolve(schema.items ?? {}, defs);
      field.items = typeOf(items);
      if (field.items === "object" && items.properties) {
        field.itemFields = Object.keys(items.properties).sort();
      }
    }
    if (type === "object" && schema.additionalProperties && !schema.properties) field.map = true;
    out.push(field);
    if (type === "object" && schema.properties && depth < MAX_DEPTH) {
      walk(schema.properties, defs, path, depth + 1, out);
    }
  }
};

export const buildCatalog = () => {
  const types = {};
  for (const file of readdirSync(SCHEMAS).filter((f) => /\.v\d+\.json$/.test(f)).sort()) {
    const schema = JSON.parse(readFileSync(join(SCHEMAS, file), "utf8"));
    const name = file.replace(/\.v\d+\.json$/, "");
    const fields = [];
    walk(schema.properties ?? {}, schema.$defs ?? {}, "data", 1, fields);
    types[name] = { version: Number(/\.v(\d+)\.json$/.exec(file)[1]), fields };
  }
  return { generatedFrom: "api/events/*.json", types };
};

const text = `${JSON.stringify(buildCatalog(), null, 1)}\n`;
if (process.argv.includes("--check")) {
  let current = "";
  try {
    current = readFileSync(OUT, "utf8");
  } catch {
    current = "";
  }
  if (current !== text) {
    console.error("eventSchemas.json is out of date: run node scripts/gen-rule-catalog.mjs");
    process.exit(1);
  }
} else {
  writeFileSync(OUT, text);
  console.log("wrote", OUT);
}
