import { ref, type Ref } from "vue";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import {
  checkAccountHealth,
  getAccountHealth,
  type AccountHealth,
} from "@/api/health";

export const useAccountHealth = (accountId: Ref<string> | (() => string)) => {
  const idOf = (): string =>
    typeof accountId === "function" ? accountId() : accountId.value;

  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const { messageOf } = useProblem();

  const health = ref<AccountHealth | null>(null);
  const loadError = ref<string | null>(null);
  const checking = ref(false);
  const checkError = ref<string | null>(null);

  const load = async (): Promise<void> => {
    const id = idOf();
    if (!id) return;
    start();
    loadError.value = null;
    try {
      health.value = await getAccountHealth(id);
      finish(true);
    } catch (e) {
      loadError.value = messageOf(e);
      finish(false);
    }
  };

  const runCheck = async (): Promise<boolean> => {
    const id = idOf();
    if (!id) return false;
    checking.value = true;
    checkError.value = null;
    try {
      health.value = await checkAccountHealth(id);
      return true;
    } catch (e) {
      checkError.value = messageOf(e);
      return false;
    } finally {
      checking.value = false;
    }
  };

  return {
    health,
    loadError,
    checkError,
    checking,
    isFirstLoad,
    isRefreshing,
    load,
    runCheck,
  };
};
