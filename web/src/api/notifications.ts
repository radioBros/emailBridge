import { api } from "@/api/client";
import type { components } from "@/api/schema";

// W75 system notifications.

export const NOTIFICATION_CATEGORIES = [
  "webhooks",
  "queues",
  "accounts",
  "sending",
  "oauth",
  "integrations",
  "pec",
  "storage",
  "platform",
  "security",
  "limits",
  "internal",
] as const;

export const NOTIFICATION_SEVERITIES = ["critical", "warning", "info"] as const;

export type NotificationCategory = (typeof NOTIFICATION_CATEGORIES)[number];
export type NotificationSeverity = (typeof NOTIFICATION_SEVERITIES)[number];
export type NotificationState = "open" | "resolved";

export type Notification = components["schemas"]["Notification"];
export type NotificationPage = components["schemas"]["Page"];
export type NotificationSummary = components["schemas"]["Summary"];

export type NotificationSort = "lastSeen" | "-lastSeen" | "firstSeen" | "-firstSeen";

export interface ListNotificationsQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: NotificationSort;
  /** Omitted = open (the server has no "all states" view). */
  state?: NotificationState;
  category?: string;
  severity?: string;
  unread?: boolean;
}

const clean = (q: ListNotificationsQuery): Record<string, string | number | boolean> => {
  const out: Record<string, string | number | boolean> = {};
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    out[k] = v as string | number | boolean;
  }
  return out;
};

export const listNotifications = async (
  q: ListNotificationsQuery = {},
): Promise<NotificationPage> => {
  const res = await api.raw.get<NotificationPage>("/notifications", { params: clean(q) });
  return res.data;
};

export const getNotificationsSummary = (): Promise<NotificationSummary> =>
  api.get<NotificationSummary>("/notifications/summary");

/** POST /v1/notifications/read — at most 200 ids per call (the caller chunks). */
export const markNotificationsRead = async (ids: string[]): Promise<void> => {
  for (let i = 0; i < ids.length; i += 200) {
    await api.post("/notifications/read", { ids: ids.slice(i, i + 200) });
  }
};

export const markAllNotificationsRead = async (): Promise<void> => {
  await api.post("/notifications/read-all", {});
};

export const resolveNotification = (id: string): Promise<Notification> =>
  api.post<Notification>(`/notifications/${encodeURIComponent(id)}/resolve`, {});

// ---- preferences (/v1/me/settings → notifications) -------------------------------------

export type DigestFrequency = "immediate" | "hourly" | "daily";

/** Settings shape under /v1/me/settings → notifications (not a dedicated OpenAPI schema). */
export interface NotificationPrefs {
  email?: {
    enabled?: boolean;
    digest?: DigestFrequency;
    minSeverity?: NotificationSeverity;
    categories?: Partial<Record<NotificationCategory, boolean>>;
  };
  mutedCategories?: string[];
  quietHours?: { start?: string; end?: string; tz?: string } | null;
}

interface MeSettingsBody {
  settings?: { notifications?: NotificationPrefs; [key: string]: unknown };
}

export const getNotificationPrefs = async (): Promise<NotificationPrefs> => {
  const body = await api.get<MeSettingsBody>("/me/settings");
  return body?.settings?.notifications ?? {};
};

/** Deep-merge PATCH (RFC 7396: `null` deletes). */
export const patchNotificationPrefs = async (
  patch: Record<string, unknown>,
): Promise<NotificationPrefs> => {
  const body = await api.patch<MeSettingsBody>("/me/settings", { notifications: patch });
  return body?.settings?.notifications ?? {};
};
