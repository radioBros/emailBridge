// i18n-check: compares one or more locales against English (the source of truth).
// Usage (from the repo root): node <this file> ru            (or: es pt | fr de | zh ja | all)
// Checks: every English file has a counterpart; same keys (no missing / extra); same {placeholders};
// no empty strings; no vue-i18n special characters (@ | { }) beyond what English uses; strings that are
// identical to English are listed as a warning (fine for names like IMAP, suspicious for sentences);
// system-mail templates exist with the same Go template actions ({{...}}).
// Exit code 1 on any error.
import fs from "node:fs";
import path from "node:path";

const ALL = ["it", "es", "pt", "fr", "de", "ru", "zh", "ja"];
let targets = process.argv.slice(2);
if (targets.length === 0 || targets.includes("all")) targets = ALL;

const root = process.cwd();
const webSrc = path.join(root, "web", "src");
const errors = [];
const warnings = [];
/** Typical sequences of UTF-8 bytes misread as Windows-1252 (never valid in our locales). */
const MOJIBAKE = /Ã[\u0080-¿¡-ÿ]|â€|Â[ -¿]|Ð[\u0080-¿]|Ñ[\u0080-¿]|[ãåæçè][\u0080-¿]/;

const flatten = (obj, prefix = "") => {
  const out = {};
  for (const [k, v] of Object.entries(obj)) {
    const p = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) Object.assign(out, flatten(v, p));
    else out[p] = v;
  }
  return out;
};

const readJSON = (file) => {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (e) {
    errors.push(`${path.relative(root, file)}: invalid JSON (${e.message})`);
    return null;
  }
};

const placeholders = (s) => [...String(s).matchAll(/\{\s*([a-zA-Z_][\w.]*)\s*\}/g)].map((m) => m[1]).sort();
const specials = (s) => {
  const t = String(s).replace(/\{\s*[a-zA-Z_][\w.]*\s*\}/g, "").replace(/\{'[^']*'\}/g, "");
  return { at: (t.match(/@/g) || []).length, pipe: (t.match(/\|/g) || []).length, brace: (t.match(/[{}]/g) || []).length };
};

// Pairs of [english file, file for locale X].
const pairsFor = (loc) => {
  const pairs = [];
  const langEn = path.join(webSrc, "lang", "en");
  for (const f of fs.readdirSync(langEn).filter((n) => n.endsWith(".json"))) {
    pairs.push([path.join(langEn, f), path.join(webSrc, "lang", loc, f)]);
  }
  const feats = path.join(webSrc, "features");
  for (const feat of fs.readdirSync(feats)) {
    const en = path.join(feats, feat, "locales", "en.json");
    if (fs.existsSync(en)) pairs.push([en, path.join(feats, feat, "locales", `${loc}.json`)]);
  }
  return pairs;
};

for (const loc of targets) {
  let keys = 0;
  let same = 0;
  for (const [enFile, locFile] of pairsFor(loc)) {
    const rel = path.relative(root, locFile);
    if (!fs.existsSync(locFile)) {
      errors.push(`${rel}: missing file`);
      continue;
    }
    const en = readJSON(enFile);
    const tr = readJSON(locFile);
    if (!en || !tr) continue;
    const fe = flatten(en);
    const ft = flatten(tr);
    for (const k of Object.keys(fe)) {
      if (!(k in ft)) {
        errors.push(`${rel}: missing key ${k}`);
        continue;
      }
      keys++;
      const a = fe[k];
      const b = ft[k];
      if (typeof b !== "string" || (b.trim() === "" && String(a).trim() !== "")) {
        errors.push(`${rel}: empty or non-string value for ${k}`);
        continue;
      }
      // Double-encoded UTF-8 (a file read as Windows-1252 and saved again): "Ã¨", "â€™", "Ð°", "ç™»"…
      if (MOJIBAKE.test(b)) errors.push(`${rel}: double-encoded UTF-8 (mojibake) in ${k}: ${b.slice(0, 40)}`);
      const pa = placeholders(a).join(",");
      const pb = placeholders(b).join(",");
      if (pa !== pb) errors.push(`${rel}: placeholders differ for ${k}: en {${pa}} vs ${loc} {${pb}}`);
      const sa = specials(a);
      const sb = specials(b);
      if (sb.at > sa.at) errors.push(`${rel}: unescaped "@" in ${k} (write {'@'})`);
      if (sb.pipe > sa.pipe) errors.push(`${rel}: unescaped "|" in ${k} (write {'|'})`);
      if (sb.brace > sa.brace) errors.push(`${rel}: stray "{" or "}" in ${k}`);
      if (loc !== "en" && a === b && /[a-z]{3,}\s+[a-z]{3,}/i.test(a)) {
        same++;
        warnings.push(`${rel}: ${k} is identical to English: "${a.slice(0, 60)}"`);
      }
    }
    for (const k of Object.keys(ft)) if (!(k in fe)) errors.push(`${rel}: extra key ${k} (not in English)`);
  }

  // System-mail templates.
  const tplEn = path.join(root, "internal", "sysmail", "templates", "en");
  const tplLoc = path.join(root, "internal", "sysmail", "templates", loc);
  for (const f of fs.readdirSync(tplEn)) {
    const target = path.join(tplLoc, f);
    const rel = path.relative(root, target);
    if (!fs.existsSync(target)) {
      errors.push(`${rel}: missing template`);
      continue;
    }
    const acts = (s) => [...s.matchAll(/\{\{[^}]*\}\}/g)].map((m) => m[0].replace(/\s+/g, " ")).sort().join("\n");
    if (acts(fs.readFileSync(path.join(tplEn, f), "utf8")) !== acts(fs.readFileSync(target, "utf8"))) {
      errors.push(`${rel}: Go template actions {{...}} differ from English`);
    }
  }
  console.log(`${loc}: ${keys} keys checked, ${same} sentence(s) identical to English`);
}

for (const w of warnings.slice(0, 40)) console.log(`warn  ${w}`);
if (warnings.length > 40) console.log(`warn  ... ${warnings.length - 40} more`);
for (const e of errors.slice(0, 200)) console.log(`ERROR ${e}`);
if (errors.length > 200) console.log(`ERROR ... ${errors.length - 200} more`);
console.log(errors.length ? `FAIL: ${errors.length} error(s)` : "OK");
process.exit(errors.length ? 1 : 0);
