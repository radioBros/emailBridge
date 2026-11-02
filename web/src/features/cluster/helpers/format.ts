import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE, formatDuration } from "@/helpers/Format";

/** Uptime from `startedAt` as `7h 30m` (or em-dash). */
export const formatUptime = (startedAt: string | null | undefined): string => {
  const start = DateUtil.parse(startedAt);
  if (!start) return EMPTY_VALUE;
  const minutes = Math.max(0, Math.floor(start.diffNow("minutes").minutes * -1));
  return formatDuration(minutes);
};

/** Relative "last seen" label from `seenAt`. */
export const formatSeenAt = (
  seenAt: string | null | undefined,
  locale: string,
): string => {
  const parsed = DateUtil.parse(seenAt);
  if (!parsed) return EMPTY_VALUE;
  return parsed.setLocale(locale).toRelative() ?? DateUtil.formatDateTime(seenAt);
};

/** Compact partition map as `events 2 · delivery 1`. */
export const formatPartitions = (
  partitions: Record<string, number> | null | undefined,
): string => {
  if (!partitions) return EMPTY_VALUE;
  const entries = Object.entries(partitions).filter(([, n]) => n > 0);
  if (!entries.length) return EMPTY_VALUE;
  return entries.map(([k, n]) => `${k} ${n}`).join(" · ");
};
