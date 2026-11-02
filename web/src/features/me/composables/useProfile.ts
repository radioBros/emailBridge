import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { auth } from "@/api/auth";
import { disableTotp } from "@/api/authFlow";
import { getMySettings, patchMe, patchUiSettings } from "@/api/me";
import {
  listMySessions,
  revokeMySession,
  revokeOtherSessions,
  type MeSession,
} from "@/api/sessions";
import DateUtil from "@/helpers/Date";
import { describeUserAgent } from "@/helpers/UserAgent";
import { useLoadState } from "@/composables/useLoadState";
import {
  useThemePreference,
  type ThemeChoice,
} from "@/composables/useThemePreference";
import { useSessionStore } from "@/stores/session";
import { useNotifyStore } from "@/stores/notify";
import { problemOf } from "@/api/problems";
import { bcp47Of, DEFAULT_LOCALE, isLocale } from "@/app/locales";
import { ensureLocale } from "@/app/i18n";

export interface PasskeyRow {
  id: string;
  name: string;
  credentialId: string;
  createdAt: string;
  lastUsedAt: string | null;
}

export interface SessionRow {
  id: string;
  current: boolean;
  device: string;
  ip: string;
  method: string;
  lastActiveLabel: string;
  createdAt: string;
  lastActiveAt: string;
  expiresAt: string;
  userAgent?: string;
}

export type { ThemeChoice };

const toSessionRow = (
  s: MeSession,
  locale: string,
  t: (key: string, values?: Record<string, unknown>) => string,
): SessionRow => ({
  id: s.id,
  current: Boolean(s.current),
  device: describeUserAgent(s.userAgent, t),
  ip: s.ip?.trim() || "—",
  method: s.method?.trim() || "—",
  lastActiveLabel:
    DateUtil.parse(s.lastActiveAt)?.toRelative({ locale }) ?? "—",
  createdAt: s.createdAt,
  lastActiveAt: s.lastActiveAt,
  expiresAt: s.expiresAt,
  userAgent: s.userAgent,
});

/** Profile screen data: basics, passkeys, TOTP, sessions. */
export const useProfile = () => {
  const session = useSessionStore();
  const notify = useNotifyStore();
  const { t } = useI18n();
  const { isFirstLoad, isRefreshing, start, finish, reset } = useLoadState();
  const { choice: themeChoice, setChoice: setThemeChoice, boot: bootTheme } =
    useThemePreference();

  const name = ref("");
  const localeChoice = ref(DEFAULT_LOCALE);
  const passkeys = ref<PasskeyRow[]>([]);
  const sessions = ref<SessionRow[]>([]);
  const totpEnabled = ref(false);
  const loadError = ref<string | null>(null);
  const saving = ref(false);
  const passkeyName = ref("");
  const totpPassword = ref("");
  const mutating = ref(false);

  const email = computed(() => session.profile?.email ?? session.user?.email ?? "");
  const otherSessionCount = computed(
    () => sessions.value.filter((s) => !s.current).length,
  );

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      await session.loadMe();
      name.value = session.profile?.name ?? "";
      const profileLoc = session.profile?.locale;
      localeChoice.value = isLocale(profileLoc) ? profileLoc : DEFAULT_LOCALE;

      try {
        const settings = await getMySettings();
        const ui = settings.settings?.ui ?? {};
        if (isLocale(ui.locale)) localeChoice.value = ui.locale;
      } catch {
        /* settings optional on first load */
      }

      await ensureLocale(localeChoice.value);
      await bootTheme();

      const [pkPage, sessList] = await Promise.all([
        auth.passkey.list(),
        listMySessions().catch(() => ({ data: [] as MeSession[] })),
      ]);
      const items = Array.isArray(pkPage) ? pkPage : (pkPage?.items ?? []);
      passkeys.value = items.map((p) => ({
        id: String(p.id),
        name: p.name || t("me.passkey_name"),
        credentialId: p.credentialId,
        createdAt: p.createdAt,
        lastUsedAt: p.lastUsedAt,
      }));
      totpEnabled.value = Boolean(auth.twoFactor.isTwoFactorEnabled?.());
      const u = session.user as { twoFactorEnabled?: boolean } | null;
      if (u && typeof u.twoFactorEnabled === "boolean") totpEnabled.value = u.twoFactorEnabled;

      const dateLocale = bcp47Of(localeChoice.value);
      sessions.value = (sessList.data ?? []).map((s) =>
        toSessionRow(s, dateLocale, t),
      );
      finish(true);
    } catch (err) {
      loadError.value = problemOf(err)?.detail || t("me.load_error");
      finish(false);
    }
  };

  const saveBasics = async (): Promise<boolean> => {
    saving.value = true;
    try {
      const next = isLocale(localeChoice.value) ? localeChoice.value : DEFAULT_LOCALE;
      await patchMe({
        name: name.value.trim(),
        locale: next,
      });
      await patchUiSettings({ locale: next });
      await setThemeChoice(themeChoice.value);
      await session.loadMe();
      await ensureLocale(next);
      notify.push({ message: t("me.saved"), color: "success" });
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("me.load_error"),
        color: "error",
        code: problemOf(err)?.code,
        traceId: problemOf(err)?.traceId,
      });
      return false;
    } finally {
      saving.value = false;
    }
  };

  const addPasskey = async (): Promise<boolean> => {
    mutating.value = true;
    try {
      await auth.passkey.register({
        name: passkeyName.value.trim() || undefined,
      });
      passkeyName.value = "";
      reset();
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("auth.error_passkey_failed"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const removePasskey = async (row: PasskeyRow): Promise<boolean> => {
    mutating.value = true;
    try {
      await auth.passkey.delete({ id: row.id, credentialId: row.credentialId });
      passkeys.value = passkeys.value.filter((p) => p.id !== row.id);
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("me.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const disableAuthenticator = async (): Promise<boolean> => {
    mutating.value = true;
    try {
      await disableTotp(totpPassword.value);
      totpPassword.value = "";
      totpEnabled.value = false;
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("me.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const revokeSession = async (id: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await revokeMySession(id);
      sessions.value = sessions.value.filter((s) => s.id !== id);
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("me.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  /** Revoke every other session; keep this device signed in. */
  const revokeOthers = async (): Promise<boolean> => {
    mutating.value = true;
    try {
      await revokeOtherSessions();
      sessions.value = sessions.value.filter((s) => s.current);
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("me.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  return {
    name,
    email,
    localeChoice,
    themeChoice,
    passkeys,
    sessions,
    otherSessionCount,
    totpEnabled,
    loadError,
    saving,
    passkeyName,
    totpPassword,
    mutating,
    isFirstLoad,
    isRefreshing,
    load,
    saveBasics,
    addPasskey,
    removePasskey,
    disableAuthenticator,
    revokeSession,
    revokeOthers,
  };
};
