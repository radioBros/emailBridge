import { EMPTY_VALUE } from "@/helpers/Format";

/**
 * Masked API-key label for lists: `prefix…last4` (e.g. `ebk_…9f2c`).
 * The API always sends both fields; a missing or blank part is dropped rather than
 * rendered as the literal "undefined", and nothing at all falls back to EMPTY_VALUE.
 */
export const maskedKey = (prefix?: string | null, last4?: string | null): string => {
  const head = (prefix ?? "").trim();
  const tail = (last4 ?? "").trim();
  if (!head && !tail) return EMPTY_VALUE;
  return `${head}…${tail}`;
};
