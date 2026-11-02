/** What the test panel evaluates the rule against. */
export type TestSource =
  | { mode: "recent"; eventId: string }
  | { mode: "paste"; json: string }
  | { mode: "sample"; type: string };

/** Request body part for a source; null when the source is not usable yet. */
export const sourceBody = (
  s: TestSource,
): { eventId: string } | { event: Record<string, unknown> } | { sample: string } | null => {
  if (s.mode === "recent") return s.eventId ? { eventId: s.eventId } : null;
  if (s.mode === "sample") return s.type ? { sample: s.type } : null;
  try {
    const v = JSON.parse(s.json) as unknown;
    return v && typeof v === "object" && !Array.isArray(v) ? { event: v as Record<string, unknown> } : null;
  } catch {
    return null;
  }
};
