import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  getSettings,
  listFeatureSwitches,
  patchSettings,
  type FeatureSwitchBody,
  type SettingsBody,
  type SettingsScope,
  type SettingValue,
} from "@/api/settings";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import {
  SETTINGS_FIELDS,
  type SettingFieldDef,
  type SettingsTabId,
  valuesEqual,
} from "@/features/settings/catalog";

export const useSettings = () => {
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const session = useSessionStore();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const body = ref<SettingsBody | null>(null);
  const etag = ref<string | null>(null);
  const switches = ref<FeatureSwitchBody[]>([]);
  const drafts = ref<Record<string, unknown>>({});
  const switchDrafts = ref<Record<string, unknown>>({});
  const scope = ref<SettingsScope>("organization");
  const tab = ref<SettingsTabId>("general");
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const saving = ref(false);
  /** Settings quick search. While non-empty, the page shows one result list instead of the tabs. */
  const query = ref("");
  const searching = computed(() => query.value.trim() !== "");

  const canWriteOrg = computed(() => {
    const role = session.me?.role ?? session.profile?.instanceRole;
    return role === "owner" || role === "admin";
  });

  const canWriteGlobal = computed(
    () => session.profile?.instanceRole === "owner",
  );

  const canWrite = computed(() =>
    scope.value === "global" ? canWriteGlobal.value : canWriteOrg.value,
  );

  const fieldsForTab = computed((): SettingFieldDef[] => {
    if (tab.value === "switches") return [];
    return SETTINGS_FIELDS[tab.value] ?? [];
  });

  const entryOf = (key: string): SettingValue | undefined => body.value?.values?.[key];

  const switchEntryOf = (name: string): SettingValue | undefined =>
    body.value?.switches?.[name];

  const isLocked = (key: string, field?: SettingFieldDef): boolean => {
    const entry = entryOf(key);
    if (entry?.locked) return true;
    const change =
      field?.changeScope ??
      (entry?.scope as SettingFieldDef["changeScope"]) ??
      "both";
    if (change === "global" && scope.value !== "global") return true;
    if (change === "organization" && scope.value !== "organization") return true;
    if (!canWrite.value) return true;
    return false;
  };

  const isSwitchLocked = (sw: FeatureSwitchBody): boolean => {
    if (sw.locked) return true;
    if (!canWrite.value) return true;
    const scopes = sw.scopes ?? [];
    if (scope.value === "organization" && !scopes.includes("organization")) return true;
    if (scope.value === "global" && !scopes.includes("global")) return true;
    return false;
  };

  const draftValue = (key: string): unknown => {
    if (Object.prototype.hasOwnProperty.call(drafts.value, key)) return drafts.value[key];
    return entryOf(key)?.value;
  };

  const draftSwitch = (name: string): unknown => {
    if (Object.prototype.hasOwnProperty.call(switchDrafts.value, name)) {
      return switchDrafts.value[name];
    }
    const fromList = switches.value.find((s) => s.name === name);
    return fromList?.effective ?? switchEntryOf(name)?.value;
  };

  const setDraft = (key: string, value: unknown): void => {
    drafts.value = { ...drafts.value, [key]: value };
  };

  const setSwitchDraft = (name: string, value: unknown): void => {
    switchDrafts.value = { ...switchDrafts.value, [name]: value };
  };

  const dirtyKeys = computed(() => {
    const keys: string[] = [];
    for (const key of Object.keys(drafts.value)) {
      if (!valuesEqual(drafts.value[key], entryOf(key)?.value)) keys.push(key);
    }
    return keys;
  });

  const dirtySwitchKeys = computed(() => {
    const keys: string[] = [];
    for (const name of Object.keys(switchDrafts.value)) {
      const original =
        switches.value.find((s) => s.name === name)?.effective ??
        switchEntryOf(name)?.value;
      if (!valuesEqual(switchDrafts.value[name], original)) keys.push(name);
    }
    return keys;
  });

  /**
   * Every unsaved draft counts, whichever tab (or search result) it was made on, so an edit is never
   * hidden by switching tab and Save always sends all of them.
   */
  const isDirty = computed(() => dirtyKeys.value.length + dirtySwitchKeys.value.length > 0);

  const discard = (): void => {
    drafts.value = {};
    switchDrafts.value = {};
  };

  const applyBody = (next: SettingsBody, nextEtag: string | null): void => {
    body.value = next;
    etag.value = nextEtag ?? (next.version != null ? `v${next.version}` : etag.value);
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const [settingsRes, swRes] = await Promise.all([
        getSettings(),
        listFeatureSwitches(),
      ]);
      applyBody(settingsRes.body, settingsRes.etag);
      switches.value = swRes.data ?? [];
      discard();
      finish(true);
    } catch (err) {
      if (err instanceof ApiProblemError && err.problem.status === 403) {
        forbidden.value = true;
      } else {
        loadError.value = messageOf(err);
      }
      finish(false);
    }
  };

  const saveTab = async (): Promise<void> => {
    if (!canWrite.value || !isDirty.value) return;
    saving.value = true;
    try {
      const patch: Record<string, unknown> = {};
      // One patch with every draft: settings of all tabs and the feature switches.
      for (const name of dirtySwitchKeys.value) {
        patch[name] = switchDrafts.value[name];
      }
      for (const key of dirtyKeys.value) {
        patch[key] = drafts.value[key];
      }
      const res = await patchSettings(scope.value, patch, etag.value);
      applyBody(res.body, res.etag);
      const swRes = await listFeatureSwitches();
      switches.value = swRes.data ?? [];
      discard();
      notify.push({ message: t("settings.saved"), color: "success" });
    } catch (err) {
      notify.push({ message: messageOf(err), color: "error" });
    } finally {
      saving.value = false;
    }
  };

  return {
    body,
    etag,
    switches,
    scope,
    tab,
    loadError,
    forbidden,
    saving,
    query,
    searching,
    isFirstLoad,
    isRefreshing,
    canWrite,
    canWriteGlobal,
    canWriteOrg,
    fieldsForTab,
    entryOf,
    switchEntryOf,
    isLocked,
    isSwitchLocked,
    draftValue,
    draftSwitch,
    setDraft,
    setSwitchDraft,
    dirtyKeys,
    dirtySwitchKeys,
    isDirty,
    discard,
    load,
    saveTab,
  };
};
