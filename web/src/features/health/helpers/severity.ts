import {
  mdiAlertCircle,
  mdiCheckCircle,
  mdiCloseCircle,
  mdiInformation,
} from "@mdi/js";
import type { StatusColor } from "@/constants/Statuses";
import type { HealthSeverity } from "@/api/health";

export const SEVERITIES: HealthSeverity[] = ["ok", "info", "warning", "critical"];

export const severityColor = (sev: string): StatusColor => {
  switch (sev) {
    case "critical":
      return "error";
    case "warning":
      return "warning";
    case "info":
      return "info";
    case "ok":
      return "success";
    default:
      return "grey";
  }
};

export const severityIcon = (sev: string): string => {
  switch (sev) {
    case "critical":
      return mdiCloseCircle;
    case "warning":
      return mdiAlertCircle;
    case "info":
      return mdiInformation;
    default:
      return mdiCheckCircle;
  }
};

export const severityLabelKey = (sev: string): string => `health.severity_${sev}`;

export const hintTitleKey = (code: string): string => `health.hint_${code}_title`;
export const hintDetailKey = (code: string): string => `health.hint_${code}_detail`;

export const sortParam = (key: string, order: "asc" | "desc"): string => {
  const allowed = new Set(["severity", "name", "state", "lastSyncAt"]);
  if (!allowed.has(key)) return "-severity";
  return order === "desc" ? `-${key}` : key;
};
