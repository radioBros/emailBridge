import { describe, expect, it } from "vitest";
import {
  highlightSegments,
  matchRanges,
  mergeRanges,
  normalise,
  searchEntries,
  tokenize,
  type SearchEntry,
} from "@/features/settings/helpers/search";

const entry = (id: string, label: string, description = "", key = id, tab = "General"): SearchEntry => ({
  id,
  label,
  description,
  key,
  tab,
});

const marked = (text: string, query: string): string[] =>
  highlightSegments(text, matchRanges(text, tokenize(query)))
    .filter((s) => s.match)
    .map((s) => s.text);

describe("settings search: normalisation", () => {
  it("is case- and accent-insensitive", () => {
    expect(normalise("Fréquence ÉLEVÉE")).toBe("frequence elevee");
    expect(normalise("Größe")).toBe("große");
    expect(normalise("İstanbul")).toBe("istanbul");
  });

  it("splits the query into distinct words", () => {
    expect(tokenize("  Webhook   TIMEOUT webhook ")).toEqual(["webhook", "timeout"]);
    expect(tokenize("   ")).toEqual([]);
  });
});

describe("settings search: highlight ranges", () => {
  it("maps matches on accented text back to the original characters", () => {
    expect(marked("Délai de reprise", "delai")).toEqual(["Délai"]);
    expect(marked("Paramètres réseau", "reseau param")).toEqual(["Param", "réseau"]);
  });

  it("maps correctly after a character that lowercases to two units", () => {
    // "İ" lowercases to "i" + combining dot; the dot is stripped, indices stay exact.
    expect(marked("İzin süresi", "sure")).toEqual(["süre"]);
    expect(matchRanges("İzin süresi", ["sure"])).toEqual([{ start: 5, end: 9 }]);
  });

  it("merges overlapping and touching ranges", () => {
    expect(
      mergeRanges([
        { start: 4, end: 6 },
        { start: 0, end: 2 },
        { start: 2, end: 3 },
        { start: 5, end: 8 },
      ]),
    ).toEqual([
      { start: 0, end: 3 },
      { start: 4, end: 8 },
    ]);
    expect(marked("timeout", "time meout")).toEqual(["timeout"]);
  });

  it("finds every occurrence and splits into plain + matched segments", () => {
    expect(highlightSegments("abcabc", matchRanges("abcabc", ["b"]))).toEqual([
      { text: "a", match: false },
      { text: "b", match: true },
      { text: "ca", match: false },
      { text: "b", match: true },
      { text: "c", match: false },
    ]);
    expect(highlightSegments("plain", [])).toEqual([{ text: "plain", match: false }]);
  });
});

describe("settings search: matching and order", () => {
  const entries: SearchEntry[] = [
    entry("a", "Session lifetime", "", "authSessionTtl", "Security"),
    entry("b", "Lockout duration", "How long a session stays locked", "authLockoutDuration", "Security"),
    entry("c", "Webhook timeout", "", "webhookTimeout", "Webhooks & egress"),
    entry("d", "Max IDLE", "", "syncMaxIdle", "Sync defaults"),
    entry("e", "Events", "0 = keep", "retentionEvents", "Retention"),
    entry("f", "Sandbox mode", "Capture outgoing mail", "sandbox.enabled", "Feature switches"),
  ];

  it("requires every word to match, in any order", () => {
    expect(searchEntries(entries, "timeout webhook").map((r) => r.entry.id)).toEqual(["c"]);
    expect(searchEntries(entries, "webhook nothing-like-this")).toEqual([]);
    expect(searchEntries(entries, "")).toEqual([]);
  });

  it("matches the technical key", () => {
    expect(searchEntries(entries, "syncmaxidle").map((r) => r.entry.id)).toEqual(["d"]);
    expect(searchEntries(entries, "sandbox.enabled")[0]?.rank).toBe(2);
  });

  it("matches the tab name", () => {
    const res = searchEntries(entries, "retention");
    // "Events" matches via its key (retentionEvents) and the tab name; key ranks first.
    expect(res.map((r) => r.entry.id)).toEqual(["e"]);
    expect(res[0]?.rank).toBe(2);
    expect(searchEntries(entries, "egress").map((r) => [r.entry.id, r.rank])).toEqual([["c", 3]]);
  });

  it("orders label matches, then description, then key, stable within a group", () => {
    const res = searchEntries(entries, "session");
    expect(res.map((r) => [r.entry.id, r.rank])).toEqual([
      ["a", 0],
      ["b", 1],
    ]);
    const multi = searchEntries(
      [
        entry("k1", "Other", "", "xSessionX"),
        entry("d1", "Other", "session here"),
        entry("l2", "Session two"),
        entry("l1", "Session one"),
      ],
      "session",
    );
    expect(multi.map((r) => r.entry.id)).toEqual(["l2", "l1", "d1", "k1"]);
  });

  it("returns highlight ranges per field", () => {
    const [res] = searchEntries(entries, "lock");
    expect(res?.entry.id).toBe("b");
    expect(res?.ranges.label).toEqual([{ start: 0, end: 4 }]);
    expect(res?.ranges.description).toEqual([{ start: 25, end: 29 }]);
    expect(res?.ranges.key).toEqual([{ start: 4, end: 8 }]);
    expect(res?.ranges.tab).toEqual([]);
  });
});
