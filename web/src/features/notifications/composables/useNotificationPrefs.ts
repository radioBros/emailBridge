import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  getNotificationPrefs,
  NOTIFICATION_CATEGORIES,
  patchNotificationPrefs,
  type DigestFrequency,
  type NotificationCategory,
  type NotificationPrefs,
  type NotificationSeverity,
} from "@/api/notifications";
import { listFeatureSwitches } from "@/api/settings";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";

export const EMAIL_SWITCH = "notifications.email";

/** Owner decision 2026-10-27: alert emails go to owners and admins by default; others opt in. */
export const emailDefaultForRole = (role: string | null | undefined): boolean =>
  role === "owner" || role === "admin";

export interface PrefsDraft {
  emailEnabled: boolean;
  digest: DigestFrequency;
  minSeverity: NotificationSeverity;
  /** Per category: emailed (email.categories) and shown in the bell (not in mutedCategories). */
  email: Record<NotificationCategory, boolean>;
  inApp: Record<NotificationCategory, boolean>;
  quietEnabled: boolean;
  quietStart: string;
  quietEnd: string;
  quietTz: string;
}

const HHMM = /^([01]\d|2[0-3]):[0-5]\d$/;

export const isTime = (v: string): boolean => HHMM.test(v);

const browserZone = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
};

const allCategories = (value: boolean): Record<NotificationCategory, boolean> =>
  Object.fromEntries(NOTIFICATION_CATEGORIES.map((c) => [c, value])) as Record<NotificationCategory, boolean>;

/** Saved preferences → the form, filling every gap with the documented default. */
export const draftOf = (p: NotificationPrefs, role: string | null | undefined): PrefsDraft => {
  const email = allCategories(true);
  for (const [k, v] of Object.entries(p.email?.categories ?? {})) {
    if (k in email && typeof v === "boolean") email[k as NotificationCategory] = v;
  }
  const muted = new Set(p.mutedCategories ?? []);
  const inApp = allCategories(true);
  for (const c of NOTIFICATION_CATEGORIES) inApp[c] = !muted.has(c);
  const qh = p.quietHours ?? null;
  const digest = p.email?.digest;
  const sev = p.email?.minSeverity;
  return {
    emailEnabled: typeof p.email?.enabled === "boolean" ? p.email.enabled : emailDefaultForRole(role),
    digest: digest === "immediate" || digest === "daily" ? digest : "hourly",
    minSeverity: sev === "info" || sev === "critical" ? sev : "warning",
    email,
    inApp,
    quietEnabled: Boolean(qh && qh.start && qh.end),
    quietStart: qh?.start || "22:00",
    quietEnd: qh?.end || "07:00",
    quietTz: qh?.tz || browserZone(),
  };
};

/**
 * The PATCH body for `/v1/me/settings → notifications`. `email.enabled` is written only when the
 * user changed it or had saved it before, so an untouched toggle keeps following the role default.
 */
export const patchOf = (d: PrefsDraft, opts: { writeEnabled: boolean }): Record<string, unknown> => {
  const email: Record<string, unknown> = {
    digest: d.digest,
    minSeverity: d.minSeverity,
    categories: { ...d.email },
  };
  if (opts.writeEnabled) email.enabled = d.emailEnabled;
  return {
    email,
    mutedCategories: NOTIFICATION_CATEGORIES.filter((c) => !d.inApp[c]),
    quietHours: d.quietEnabled ? { start: d.quietStart, end: d.quietEnd, tz: d.quietTz } : null,
  };
};

/** Notification preferences card state: load, draft, dirty, save; plus the organisation switch. */
export const useNotificationPrefs = () => {
  const { t } = useI18n();
  const session = useSessionStore();
  const notify = useNotifyStore();
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const saved = ref<NotificationPrefs>({});
  const draft = ref<PrefsDraft>(draftOf({}, null));
  const baseline = ref<string>("");
  const loadError = ref<string | null>(null);
  const saving = ref(false);
  /** Effective value of the `notifications.email` switch for the organisation (null = unknown). */
  const orgEmailOn = ref<boolean | null>(null);

  const role = computed(() => session.me?.role ?? null);
  const roleDefault = computed(() => emailDefaultForRole(role.value));
  /** True while the toggle shows the role default rather than a saved choice. */
  const usingRoleDefault = computed(() => typeof saved.value.email?.enabled !== "boolean");
  const enabledTouched = computed(() => draft.value.emailEnabled !== roleDefault.value || !usingRoleDefault.value);

  const dirty = computed(() => JSON.stringify(draft.value) !== baseline.value);
  const quietValid = computed(
    () => !draft.value.quietEnabled || (isTime(draft.value.quietStart) && isTime(draft.value.quietEnd)),
  );
  const emailBlocked = computed(() => orgEmailOn.value === false);

  const reset = (): void => {
    draft.value = draftOf(saved.value, role.value);
    baseline.value = JSON.stringify(draft.value);
  };

  const loadSwitch = async (): Promise<void> => {
    try {
      const res = await listFeatureSwitches();
      const sw = (res?.data ?? []).find((s) => s.name === EMAIL_SWITCH);
      orgEmailOn.value = sw ? !(sw.effective === false || sw.effective === "false") : null;
    } catch {
      orgEmailOn.value = null;
    }
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const [prefs] = await Promise.all([getNotificationPrefs(), loadSwitch()]);
      saved.value = prefs ?? {};
      reset();
      finish(true);
    } catch (err) {
      loadError.value = messageOf(err);
      finish(false);
    }
  };

  const save = async (): Promise<boolean> => {
    if (saving.value || !quietValid.value) return false;
    saving.value = true;
    try {
      const next = await patchNotificationPrefs(patchOf(draft.value, { writeEnabled: enabledTouched.value }));
      saved.value = next ?? {};
      reset();
      notify.push({ message: t("notifications.prefs.saved"), color: "success" });
      return true;
    } catch (err) {
      notify.push({ message: messageOf(err), color: "error" });
      return false;
    } finally {
      saving.value = false;
    }
  };

  return {
    draft,
    saved,
    loadError,
    saving,
    dirty,
    quietValid,
    orgEmailOn,
    emailBlocked,
    roleDefault,
    usingRoleDefault,
    isFirstLoad,
    isRefreshing,
    load,
    save,
    reset,
  };
};
