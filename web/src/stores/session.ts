import { computed, ref, watch } from "vue";
import { defineStore } from "pinia";
import { auth } from "@/api/auth";
import { configureApiClient } from "@/api/client";
import { getMe, type MeResponse, type MeUser } from "@/api/me";
import { useSettingsStore } from "@/stores/settings";
import { isProblemCode } from "@/api/problems";

export type { MeUser, MeResponse };

export const useSessionStore = defineStore("session", () => {
  const me = ref<MeResponse | null>(null);
  const meLoading = ref(false);
  const meError = ref<unknown>(null);
  const organizationId = ref<string | null>(null);
  /** True when the session is valid but MFA enrolment is still required (S10 §2.2). */
  const needsEnrolment = ref(false);
  /** True after loadMe has resolved enrolment for the current user. */
  const enrolmentChecked = ref(false);

  const session = auth.useSession();

  const isAuthenticated = computed(() => Boolean(session.value.data?.user));
  const user = computed(() => session.value.data?.user ?? null);
  const profile = computed<MeUser | null>(() => me.value?.user ?? null);
  const isPending = computed(() => session.value.isPending);

  configureApiClient({
    getOrganizationId: () => organizationId.value ?? me.value?.activeOrganizationId,
    onUnauthorized: () => {
      me.value = null;
      needsEnrolment.value = false;
      if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
        window.location.assign("/login");
      }
    },
  });

  const detectEnrolment = async (): Promise<boolean> => {
    if (!isAuthenticated.value) {
      needsEnrolment.value = false;
      enrolmentChecked.value = true;
      return false;
    }
    // Session list is blocked with mfa_required while enrolment is pending (S10 / auth tests).
    // Only a real JSON success clears the flag — SPA HTML fallbacks must not count as enrolled.
    try {
      const res = await fetch("/v1/auth/sessions", {
        credentials: "include",
        headers: { Accept: "application/json", "X-Requested-With": "EmailBridge" },
      });
      const ct = res.headers.get("content-type") ?? "";
      let body: unknown;
      try {
        body = ct.includes("application/json") ? await res.json() : null;
      } catch {
        body = null;
      }
      if (res.ok && Array.isArray(body)) {
        needsEnrolment.value = false;
        enrolmentChecked.value = true;
        return false;
      }
      const code =
        body && typeof body === "object" && "code" in body
          ? String((body as { code?: string }).code ?? "")
          : "";
      if (code === "mfa_required" || (res.status === 401 && me.value?.mfa === false)) {
        needsEnrolment.value = true;
        enrolmentChecked.value = true;
        return true;
      }
      needsEnrolment.value = me.value?.mfa === false;
      enrolmentChecked.value = true;
      return needsEnrolment.value;
    } catch {
      needsEnrolment.value = me.value?.mfa === false;
      enrolmentChecked.value = true;
      return needsEnrolment.value;
    }
  };

  const loadMe = async (): Promise<void> => {
    if (!isAuthenticated.value) {
      me.value = null;
      needsEnrolment.value = false;
      enrolmentChecked.value = true;
      return;
    }
    meLoading.value = true;
    meError.value = null;
    enrolmentChecked.value = false;
    try {
      me.value = await getMe();
      if (!organizationId.value && me.value.activeOrganizationId) {
        organizationId.value = me.value.activeOrganizationId;
      }
      if (me.value.mfa) {
        needsEnrolment.value = false;
        enrolmentChecked.value = true;
      } else {
        await detectEnrolment();
      }
    } catch (err) {
      meError.value = err;
      if (isProblemCode(err, "mfa_required")) {
        needsEnrolment.value = true;
        enrolmentChecked.value = true;
      } else {
        me.value = null;
        enrolmentChecked.value = true;
      }
    } finally {
      meLoading.value = false;
    }
  };

  const setOrganization = (id: string | null): void => {
    organizationId.value = id;
  };

  const markEnrolled = (): void => {
    needsEnrolment.value = false;
    enrolmentChecked.value = true;
    if (me.value) me.value = { ...me.value, mfa: true };
  };

  const signOut = async (): Promise<void> => {
    await auth.signout();
    me.value = null;
    organizationId.value = null;
    needsEnrolment.value = false;
    enrolmentChecked.value = false;
    useSettingsStore().reset();
  };

  watch(
    () => session.value.data?.user?.id,
    (id) => {
      if (id) void loadMe();
      else {
        me.value = null;
        needsEnrolment.value = false;
        enrolmentChecked.value = false;
      }
    },
    { immediate: true },
  );

  return {
    session,
    me,
    meLoading,
    meError,
    organizationId,
    needsEnrolment,
    enrolmentChecked,
    isAuthenticated,
    user,
    profile,
    isPending,
    loadMe,
    detectEnrolment,
    setOrganization,
    markEnrolled,
    signOut,
  };
});
