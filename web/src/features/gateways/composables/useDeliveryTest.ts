import { ref } from "vue";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { listAccounts, type AccountView } from "@/api/accounts";
import {
  createDeliveryTest,
  getDeliveryTest,
  isTerminalDeliveryTest,
  type DeliveryTestView,
} from "@/api/deliveryTests";

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export const useDeliveryTest = () => {
  const { messageOf } = useProblem();
  const notify = useNotifyStore();

  const accounts = ref<AccountView[]>([]);
  const accountsLoading = ref(false);
  const accountId = ref<string | null>(null);
  const to = ref("");
  const running = ref(false);
  const result = ref<DeliveryTestView | null>(null);
  const error = ref<string | null>(null);
  let pollGen = 0;

  const loadAccounts = async (q = ""): Promise<void> => {
    accountsLoading.value = true;
    try {
      const res = await listAccounts({ q: q || undefined, limit: 50, includeTotal: false });
      accounts.value = res.data ?? [];
    } catch (err) {
      accounts.value = [];
      notify.push({ message: messageOf(err), color: "error" });
    } finally {
      accountsLoading.value = false;
    }
  };

  const accountLabel = (id: string): string => {
    const a = accounts.value.find((x) => x.id === id);
    return a ? a.name : id;
  };

  const start = async (): Promise<DeliveryTestView | null> => {
    if (!accountId.value || !to.value.trim()) {
      error.value = "required";
      return null;
    }
    running.value = true;
    error.value = null;
    result.value = null;
    const gen = ++pollGen;
    try {
      const accepted = await createDeliveryTest({
        accountId: accountId.value,
        to: to.value.trim(),
      });
      let view = await getDeliveryTest(accepted.id);
      result.value = view;
      while (!isTerminalDeliveryTest(view.state) && gen === pollGen) {
        await sleep(1500);
        if (gen !== pollGen) return null;
        view = await getDeliveryTest(accepted.id);
        result.value = view;
      }
      return view;
    } catch (e) {
      error.value = messageOf(e);
      return null;
    } finally {
      if (gen === pollGen) running.value = false;
    }
  };

  const reset = (): void => {
    pollGen += 1;
    running.value = false;
    result.value = null;
    error.value = null;
  };

  return {
    accounts,
    accountsLoading,
    accountId,
    to,
    running,
    result,
    error,
    loadAccounts,
    accountLabel,
    start,
    reset,
  };
};
