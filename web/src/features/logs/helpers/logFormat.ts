import { DateTime } from "luxon";
import type { LogRecord } from "@/api/logs";
import { EMPTY_VALUE } from "@/helpers/Format";

/** Log `time` is nanoseconds UTC. */
export const formatLogTime = (ns: number | null | undefined): string => {
  if (ns == null || !Number.isFinite(ns)) return EMPTY_VALUE;
  const ms = Math.floor(ns / 1_000_000);
  return DateTime.fromMillis(ms, { zone: "utc" }).toLocal().toFormat("dd/MM/yyyy HH:mm:ss.SSS");
};

export const logRowId = (row: LogRecord): string => row.id;

export const prettyJson = (value: unknown): string => {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

export const formatBytes = (bytes: number | null | undefined): string => {
  if (bytes == null || !Number.isFinite(bytes)) return EMPTY_VALUE;
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let n = bytes;
  let i = -1;
  do {
    n /= 1024;
    i += 1;
  } while (n >= 1024 && i < units.length - 1);
  return `${n.toFixed(n >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
};

export const defaultRange = (): { from: string; to: string } => {
  const to = DateTime.utc();
  const from = to.minus({ days: 1 });
  return { from: from.toISO()!, to: to.toISO()! };
};

/** Cursor-mode total so TableFooter can page without a server total. */
export const cursorTotal = (
  page: number,
  ipp: number,
  itemCount: number,
  hasMore: boolean,
): number => {
  if (hasMore) return page * ipp + 1;
  return Math.max((page - 1) * ipp + itemCount, itemCount);
};
