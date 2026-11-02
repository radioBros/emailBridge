import { formatDate } from "@/helpers/Date";

/** Activator label: `DD/MM/YYYY` or `DD/MM/YYYY – DD/MM/YYYY`. */
export const formatRangeLabel = (from: string, to: string): string => {
  if (!from && !to) return "";
  const a = from ? formatDate(from) : "";
  const b = to ? formatDate(to) : a;
  if (!a || a === "—") return "";
  if (!b || b === "—" || b === a) return a;
  return `${a} – ${b}`;
};

export const startOfMonth = (value: Date): Date =>
  new Date(value.getFullYear(), value.getMonth(), 1);

/** Shift a month-start anchor by `delta` months (keeps day = 1). */
export const shiftMonth = (anchor: Date, delta: number): Date =>
  new Date(anchor.getFullYear(), anchor.getMonth() + delta, 1);

export const monthParts = (anchor: Date): { month: number; year: number } => ({
  month: anchor.getMonth(),
  year: anchor.getFullYear(),
});
