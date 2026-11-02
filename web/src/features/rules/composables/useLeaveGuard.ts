import { onBeforeUnmount, onMounted, type Ref } from "vue";
import { onBeforeRouteLeave } from "vue-router";
import { useI18n } from "vue-i18n";
import { useConfirm } from "@/composables/useConfirm";

/** Asks before leaving a page with unsaved rule edits (in-app navigation and tab close). */
export const useLeaveGuard = (dirty: Ref<boolean>): void => {
  const { t } = useI18n();
  const confirm = useConfirm();

  onBeforeRouteLeave(async () => {
    if (!dirty.value) return true;
    return confirm({
      title: t("rules.leave_title"),
      body: t("rules.leave_body"),
      variant: "warning",
      confirmText: t("rules.leave_confirm"),
    });
  });

  const onUnload = (e: BeforeUnloadEvent): void => {
    if (!dirty.value) return;
    e.preventDefault();
  };
  onMounted(() => window.addEventListener("beforeunload", onUnload));
  onBeforeUnmount(() => window.removeEventListener("beforeunload", onUnload));
};
