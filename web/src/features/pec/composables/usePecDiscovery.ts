import { computed, ref, watch, type Ref } from "vue";
import { getAutoconfig, type AutoconfigResult, type PECDefaults } from "@/api/pec";

const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Debounced autoconfig lookup for PEC preset suggestions on the account form. */
export const usePecDiscovery = (email: Ref<string>) => {
  const discovery = ref<AutoconfigResult | null>(null);
  const loading = ref(false);
  const applied = ref({ enabled: false, inbox: false, policy: false });
  let timer: ReturnType<typeof setTimeout> | null = null;
  let seq = 0;

  const pecDefaults = computed<PECDefaults | null>(
    () => discovery.value?.pecDefaults ?? null,
  );

  const showBanner = computed(() => Boolean(discovery.value?.pec));

  const run = async (addr: string): Promise<void> => {
    const trimmed = addr.trim();
    if (!EMAIL_RE.test(trimmed)) {
      discovery.value = null;
      applied.value = { enabled: false, inbox: false, policy: false };
      return;
    }
    const my = ++seq;
    loading.value = true;
    try {
      const res = await getAutoconfig(trimmed);
      if (my !== seq) return;
      discovery.value = res;
      applied.value = { enabled: false, inbox: false, policy: false };
    } catch {
      if (my !== seq) return;
      discovery.value = null;
    } finally {
      if (my === seq) loading.value = false;
    }
  };

  watch(
    email,
    (v) => {
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => {
        void run(v);
      }, 400);
    },
    { immediate: true },
  );

  const markApplied = (key: "enabled" | "inbox" | "policy"): void => {
    applied.value = { ...applied.value, [key]: true };
  };

  return {
    discovery,
    pecDefaults,
    loading,
    applied,
    showBanner,
    markApplied,
  };
};
