import { useI18n } from "vue-i18n";
import { switchDescriptionOf, switchLabelOf } from "@/helpers/Switches";

/** Translated feature-switch label + description for admin UI. */
export const useSwitchLabels = () => {
  const { t, te } = useI18n();
  return {
    switchLabel: (name: string): string => switchLabelOf(name, t, te),
    switchDescription: (name: string, backendDescription?: string | null): string =>
      switchDescriptionOf(name, t, te, backendDescription),
  };
};
