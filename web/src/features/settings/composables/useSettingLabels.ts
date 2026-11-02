import { useI18n } from "vue-i18n";

/**
 * Translated label + hint of a Settings field, shared by `SettingField` and the Settings search.
 * Keys like `webhooks.autoDisableAfter` contain dots, so the lookup is flat on the `settings.keys` /
 * `settings.hints` bags and never splits the key.
 */
export const useSettingLabels = () => {
  const { t, te, tm } = useI18n();

  const fromBag = (bagKey: string, fieldKey: string): string | undefined => {
    const bag = tm(bagKey);
    if (bag && typeof bag === "object" && fieldKey in (bag as object)) {
      const v = (bag as Record<string, unknown>)[fieldKey];
      return typeof v === "string" ? v : undefined;
    }
    const dotted = `${bagKey}.${fieldKey}`;
    return te(dotted) ? t(dotted) : undefined;
  };

  const fieldLabel = (key: string): string => fromBag("settings.keys", key) ?? key;

  const fieldHint = (key: string): string | undefined => fromBag("settings.hints", key);

  const tabLabel = (tab: string): string => t(`settings.tabs.${tab}`);

  return { fieldLabel, fieldHint, tabLabel };
};
