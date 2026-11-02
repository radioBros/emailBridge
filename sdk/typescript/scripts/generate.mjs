#!/usr/bin/env node
/** Regenerate OpenAPI types into src/generated/openapi.ts */
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import fs from "node:fs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repo = path.resolve(root, "../..");
const input = path.join(repo, "api/openapi/openapi.yaml");
const output = path.join(root, "src/generated/openapi.ts");
const binJs = path.join(root, "node_modules/openapi-typescript/bin/cli.js");

if (!fs.existsSync(binJs)) {
  console.error("openapi-typescript not installed; run npm install in sdk/typescript");
  process.exit(1);
}

fs.mkdirSync(path.dirname(output), { recursive: true });
const r = spawnSync(process.execPath, [binJs, input, "-o", output], {
  stdio: "inherit",
  cwd: root,
});
if (r.status !== 0) process.exit(r.status ?? 1);
console.log("wrote", path.relative(root, output));
