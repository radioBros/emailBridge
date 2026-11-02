import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const ROOT = join(import.meta.dirname, "../src");

const walk = (dir: string): string[] => {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...walk(path));
    else if (name.endsWith(".vue")) out.push(path);
  }
  return out;
};

const INPUT_TAGS = [
  "v-text-field",
  "v-select",
  "v-autocomplete",
  "v-combobox",
  "v-textarea",
  "AutocompleteSearch",
];

/** Split a Vue SFC template into opening tags for the listed components. */
const findOpenTags = (src: string, tag: string): string[] => {
  const re = new RegExp(`<(${tag})(\\s[^>]*)?>`, "gi");
  const tags: string[] = [];
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    // Skip self-closing false positives inside comments roughly.
    tags.push(m[0]);
  }
  return tags;
};

describe("no-validation rule", () => {
  it("every input without :rules carries no-validation", () => {
    const offenders: string[] = [];
    for (const file of walk(ROOT)) {
      const src = readFileSync(file, "utf8");
      const templateMatch = src.match(/<template[\s\S]*<\/template>/);
      if (!templateMatch) continue;
      const template = templateMatch[0];
      for (const tag of INPUT_TAGS) {
        for (const open of findOpenTags(template, tag)) {
          const hasRules = /:rules\s*=/.test(open) || /\brules\s*=/.test(open);
          const hasNoValidation = /no-validation/.test(open);
          if (!hasRules && !hasNoValidation) {
            offenders.push(`${file}: ${open.slice(0, 120)}`);
          }
        }
      }
    }
    expect(offenders).toEqual([]);
  });
});
