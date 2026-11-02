import {
  mdiCancel,
  mdiCheckCircle,
  mdiClockOutline,
  mdiCloseCircle,
  mdiLoading,
  mdiTimerSand,
} from "@mdi/js";
import type { StatusSpec } from "@/constants/Statuses";

export const ExportStates = {
  queued: { color: "info", icon: mdiClockOutline },
  running: { color: "info", icon: mdiLoading },
  succeeded: { color: "success", icon: mdiCheckCircle },
  failed: { color: "error", icon: mdiCloseCircle },
  cancelled: { color: "grey", icon: mdiCancel },
  expired: { color: "warning", icon: mdiTimerSand },
} as const satisfies Record<string, StatusSpec>;

export type ExportStateKey = keyof typeof ExportStates;

export const exportStatusOf = (state: string | undefined): StatusSpec =>
  (state && ExportStates[state as ExportStateKey]) || {
    color: "grey",
    icon: mdiClockOutline,
  };

export const canCancelExport = (state: string | undefined): boolean =>
  state === "queued" || state === "running";

export const canDownloadExport = (state: string | undefined): boolean =>
  state === "succeeded";

export const formatBytes = (n: number | undefined): string => {
  if (n == null || n < 0) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
};

export const progressPct = (done?: number, total?: number): number => {
  if (!total || total <= 0) return 0;
  return Math.min(100, Math.round(((done ?? 0) / total) * 100));
};
