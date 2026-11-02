import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";

const api = vi.hoisted(() => ({
  getNotificationPrefs: vi.fn(),
  patchNotificationPrefs: vi.fn(),
}));
vi.mock("@/api/notifications", async (orig) => ({
  ...(await orig<typeof import("@/api/notifications")>()),
  ...api,
}));
const switches = vi.hoisted(() => ({ listFeatureSwitches: vi.fn() }));
vi.mock("@/api/settings", () => switches);

const session = vi.hoisted(() => ({ value: null as unknown }));
vi.mock("@/stores/session", () => ({ useSessionStore: () => session.value }));

import { useNotifyStore } from "@/stores/notify";
import NotificationPreferencesCard from "@/features/notifications/components/NotificationPreferencesCard.vue";
import {
  draftOf,
  emailDefaultForRole,
  isTime,
  patchOf,
  useNotificationPrefs,
} from "@/features/notifications/composables/useNotificationPrefs";

beforeEach(() => {
  api.getNotificationPrefs.mockReset();
  api.patchNotificationPrefs.mockReset();
  switches.listFeatureSwitches.mockReset();
  switches.listFeatureSwitches.mockResolvedValue({
    data: [{ name: "notifications.email", effective: true }],
  });
  session.value = { me: { role: "viewer" } };
});

describe("preference mapping", () => {
  it("defaults the email toggle by role", () => {
    expect(emailDefaultForRole("owner")).toBe(true);
    expect(emailDefaultForRole("admin")).toBe(true);
    expect(emailDefaultForRole("operator")).toBe(false);
    expect(emailDefaultForRole("viewer")).toBe(false);
    expect(emailDefaultForRole(null)).toBe(false);
    expect(draftOf({}, "viewer").emailEnabled).toBe(false);
    expect(draftOf({}, "owner").emailEnabled).toBe(true);
    expect(draftOf({ email: { enabled: true } }, "viewer").emailEnabled).toBe(true);
  });

  it("fills defaults and reads saved values", () => {
    const d = draftOf(
      {
        email: { digest: "daily", minSeverity: "critical", categories: { queues: false, bogus: true } as never },
        mutedCategories: ["internal"],
        quietHours: { start: "21:00", end: "06:30", tz: "Europe/Rome" },
      },
      "owner",
    );
    expect(d.digest).toBe("daily");
    expect(d.minSeverity).toBe("critical");
    expect(d.email.queues).toBe(false);
    expect(d.email.webhooks).toBe(true);
    expect("bogus" in d.email).toBe(false);
    expect(d.inApp.internal).toBe(false);
    expect(d.quietEnabled).toBe(true);
    expect(d.quietTz).toBe("Europe/Rome");
    const blank = draftOf({ email: { digest: "weekly" as never, minSeverity: "loud" as never } }, "owner");
    expect(blank.digest).toBe("hourly");
    expect(blank.minSeverity).toBe("warning");
    expect(blank.quietEnabled).toBe(false);
  });

  it("builds the patch, writing enabled only when asked", () => {
    const d = draftOf({}, "viewer");
    d.inApp.queues = false;
    const p = patchOf(d, { writeEnabled: false }) as { email: Record<string, unknown>; mutedCategories: string[]; quietHours: unknown };
    expect("enabled" in p.email).toBe(false);
    expect(p.mutedCategories).toEqual(["queues"]);
    expect(p.quietHours).toBeNull();
    d.quietEnabled = true;
    const q = patchOf(d, { writeEnabled: true }) as { email: Record<string, unknown>; quietHours: unknown };
    expect(q.email.enabled).toBe(false);
    expect(q.quietHours).toEqual({ start: "22:00", end: "07:00", tz: d.quietTz });
  });

  it("validates times", () => {
    expect(isTime("07:30")).toBe(true);
    expect(isTime("24:00")).toBe(false);
    expect(isTime("7:30")).toBe(false);
  });
});

let prefs!: ReturnType<typeof useNotificationPrefs>;
const Host = defineComponent({
  setup() {
    prefs = useNotificationPrefs();
    return () => h("div");
  },
});

describe("useNotificationPrefs", () => {
  it("loads, tracks dirty and saves without freezing the role default", async () => {
    api.getNotificationPrefs.mockResolvedValue({});
    mount(Host);
    await prefs.load();
    expect(prefs.usingRoleDefault.value).toBe(true);
    expect(prefs.draft.value.emailEnabled).toBe(false);
    expect(prefs.dirty.value).toBe(false);
    expect(prefs.emailBlocked.value).toBe(false);

    prefs.draft.value.digest = "daily";
    expect(prefs.dirty.value).toBe(true);
    api.patchNotificationPrefs.mockResolvedValue({ email: { digest: "daily" } });
    expect(await prefs.save()).toBe(true);
    const body = api.patchNotificationPrefs.mock.calls[0]![0] as { email: Record<string, unknown> };
    expect("enabled" in body.email).toBe(false);
    expect(prefs.dirty.value).toBe(false);
    expect(useNotifyStore().items[0]!.color).toBe("success");

    prefs.draft.value.emailEnabled = true;
    api.patchNotificationPrefs.mockResolvedValue({ email: { enabled: true } });
    await prefs.save();
    const second = api.patchNotificationPrefs.mock.calls[1]![0] as { email: Record<string, unknown> };
    expect(second.email.enabled).toBe(true);
    expect(prefs.usingRoleDefault.value).toBe(false);
  });

  it("reports save failures and refuses invalid quiet hours", async () => {
    api.getNotificationPrefs.mockResolvedValue({});
    mount(Host);
    await prefs.load();
    prefs.draft.value.quietEnabled = true;
    prefs.draft.value.quietStart = "25:00";
    expect(prefs.quietValid.value).toBe(false);
    expect(await prefs.save()).toBe(false);
    prefs.draft.value.quietStart = "22:00";
    api.patchNotificationPrefs.mockRejectedValue(new Error("nope"));
    expect(await prefs.save()).toBe(false);
    expect(useNotifyStore().items[0]!.color).toBe("error");
    prefs.reset();
    expect(prefs.dirty.value).toBe(false);
  });

  it("knows when the organisation switched email off, and survives a load error", async () => {
    switches.listFeatureSwitches.mockResolvedValue({ data: [{ name: "notifications.email", effective: false }] });
    api.getNotificationPrefs.mockResolvedValue({});
    mount(Host);
    await prefs.load();
    expect(prefs.emailBlocked.value).toBe(true);

    switches.listFeatureSwitches.mockRejectedValue(new Error("403"));
    api.getNotificationPrefs.mockRejectedValue(new Error("down"));
    await prefs.load();
    expect(prefs.loadError.value).toBeTruthy();
    expect(prefs.orgEmailOn.value).toBeNull();
  });
});

describe("NotificationPreferencesCard", () => {
  it("shows the role hint for viewers and the org explanation when blocked", async () => {
    api.getNotificationPrefs.mockResolvedValue({});
    const w = mount(NotificationPreferencesCard);
    await flushPromises();
    expect(w.text()).toContain("notifications.prefs.email_role_hint");
    expect(w.find('[data-testid="notification-prefs-blocked"]').exists()).toBe(false);

    switches.listFeatureSwitches.mockResolvedValue({ data: [{ name: "notifications.email", effective: false }] });
    const blocked = mount(NotificationPreferencesCard);
    await flushPromises();
    expect(blocked.find('[data-testid="notification-prefs-blocked"]').exists()).toBe(true);
  });

  it("hides the role hint for owners", async () => {
    session.value = { me: { role: "owner" } };
    api.getNotificationPrefs.mockResolvedValue({});
    const w = mount(NotificationPreferencesCard);
    await flushPromises();
    expect(w.text()).toContain("notifications.prefs.email_hint");
    expect(w.text()).not.toContain("notifications.prefs.email_role_hint");
  });
});
