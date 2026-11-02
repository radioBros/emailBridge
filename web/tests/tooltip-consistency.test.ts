import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * One tooltip look app-wide: every hint is `v-tippy` (theme "eb", app/plugins/tippy.ts).
 * - no Vuetify `<v-tooltip>` (different look, different behaviour);
 * - no native `title=` used as a tooltip on controls and inline elements (browser look, no theme,
 *   no touch handling). `title` as a COMPONENT PROP (v-list-item, AppDialog, v-alert …) is a heading
 *   and is fine; `iframe title` is the frame's accessible name and is fine.
 */

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

/** Elements where `title` can only be the native browser tooltip. Aliases (`v-btn-affix`, `v-icon-inline`) included. */
const NATIVE_TITLE_TAGS = [
  "v-btn(?:-[a-z]+)*",
  "v-icon(?:-[a-z]+)*",
  "v-chip(?:-[a-z]+)*",
  "v-avatar(?:-[a-z]+)*",
  "button",
  "span",
  "kbd",
  "abbr",
  "a",
  "i",
  "div",
];

/** Quote-aware opening tag: `=>` or `a > b` inside an attribute value does not end the tag. */
const openTagRe = (tag: string): RegExp =>
  new RegExp(`<(${tag})(?=[\\s/>])((?:\\s(?:[^>"']|"[^"]*"|'[^']*')*)?)\\/?>`, "g");

const TITLE_ATTR = /(?:^|\s)(?::|v-bind:)?title\s*=/;

const templateOf = (src: string): string => {
  const start = src.indexOf("<template");
  const end = src.lastIndexOf("</template>");
  if (start < 0 || end < 0) return "";
  return src.slice(start, end);
};

const findTooltipOffences = (raw: string): string[] => {
  const template = raw.replace(/<!--[\s\S]*?-->/g, "");
  const out: string[] = [];
  for (const m of template.matchAll(/<(v-tooltip|VTooltip)(?=[\s/>])/g)) out.push(`<${m[1]}>`);
  for (const tag of NATIVE_TITLE_TAGS) {
    for (const m of template.matchAll(openTagRe(tag))) {
      if (TITLE_ATTR.test(m[2] ?? "")) out.push(m[0].replace(/\s+/g, " ").slice(0, 140));
    }
  }
  return out;
};

describe("tooltip consistency", () => {
  it("the guard catches what it should and nothing else", () => {
    const bad = `<template>
      <v-tooltip text="x"><template #activator="{ props: tip }"><v-btn v-bind="tip" /></template></v-tooltip>
      <v-btn-affix :icon="i" :title="String(item.id)" @click="() => a > b && go()" />
      <v-icon-inline :icon="mdiFlag" title="Flagged" />
      <span v-bind:title="hint">x</span>
    </template>`;
    expect(findTooltipOffences(bad)).toHaveLength(4);

    const good = `<template>
      <v-list-item :title="t('a')" @click="() => a > b" />
      <AppDialog :title="t('b')" />
      <iframe title="Preview" />
      <v-alert :title="t('c')" />
      <v-btn v-tippy="t('d')" :aria-label="t('d')" :data-title="x" />
      <span class="subtitle" :subtitle="x" />
      <!-- <v-btn :title="old" /> -->
    </template>`;
    expect(findTooltipOffences(good)).toEqual([]);
  });

  it("src uses v-tippy only: no <v-tooltip>, no native title tooltips", () => {
    const offenders: string[] = [];
    for (const file of walk(ROOT)) {
      const template = templateOf(readFileSync(file, "utf8"));
      for (const hit of findTooltipOffences(template)) offenders.push(`${relative(ROOT, file)}: ${hit}`);
    }
    expect(offenders).toEqual([]);
  });
});
