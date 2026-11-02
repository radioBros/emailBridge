import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { getSettings } from "@/api/settings";
import { searchAccounts, type AccountView } from "@/api/outbox";
import { problemCodeOf, problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import {
  FULLTEXT_SWITCH,
  SEMANTIC_MAX_LIMIT,
  searchMessages,
  semanticSearch,
  type SearchHit,
  type SemanticHit,
} from "@/features/search/helpers/api";
import { useSemanticStatus } from "@/features/search/composables/useSemanticStatus";
import { ensureAccountLabels, rememberAccounts } from "@/features/search/helpers/accountCache";

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

/** Text: the organisation-wide full-text index (G3). Meaning: semantic search in one account (G1). */
export type SearchMode = "text" | "meaning";

/** A semantic hit in the shape of the result table (no highlights: the match is by meaning, not words). */
export const semanticRow = (h: SemanticHit): SearchHit => ({
  messageId: h.messageId,
  accountId: h.accountId,
  mailboxId: h.mailboxId,
  path: h.path,
  subject: h.subject,
  from: h.from,
  to: [],
  date: h.date,
  size: 0,
  snippet: h.snippet,
  highlights: [],
  score: h.score,
});

/** The Search page: switch state, query, filters and the server-paged result list. */
export const useFullTextSearch = () => {
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const session = useSessionStore();
  /** The switch check (first paint of the page). */
  const status = useLoadState();
  /** The result list (the table's own bar shows refetches). */
  const list = useLoadState();

  /** null until known. */
  const enabled = ref<boolean | null>(null);
  const statusError = ref<string | null>(null);

  const q = ref(readQuery(route.query.q));
  const accountId = ref(readQuery(route.query.accountId));
  const from = ref(readQuery(route.query.from));
  const to = ref(readQuery(route.query.to));
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);

  const items = ref<SearchHit[]>([]);
  const total = ref(0);
  const totalCapped = ref(false);
  const loadError = ref<string | null>(null);

  /** Meaning mode needs semantic search on for the picked account. */
  const semantic = useSemanticStatus();
  const mode = ref<SearchMode>(route.query.mode === "meaning" ? "meaning" : "text");

  const accountOptions = ref<AccountView[]>([]);
  const accountsLoading = ref(false);

  /** Owners and admins (of the organisation or the instance) can turn the switch on. */
  const canManageSwitches = computed(() => {
    const role = session.me?.role ?? session.profile?.instanceRole;
    return role === "owner" || role === "admin";
  });

  const hasQuery = computed(() => q.value.trim() !== "");
  const hasFilters = computed(() => Boolean(accountId.value || from.value || to.value));

  const syncUrl = (): void => {
    const query: Record<string, string> = {};
    if (q.value.trim()) query.q = q.value.trim();
    if (accountId.value) query.accountId = accountId.value;
    if (from.value) query.from = from.value;
    if (to.value) query.to = to.value;
    if (mode.value === "meaning") query.mode = "meaning";
    if (page.value > 1) query.page = String(page.value);
    if (ipp.value !== 25) query.limit = String(ipp.value);
    void router.replace({ query });
  };

  const checkEnabled = async (): Promise<void> => {
    status.start();
    statusError.value = null;
    try {
      const { body } = await getSettings();
      enabled.value = body.switches?.[FULLTEXT_SWITCH]?.value === true;
      status.finish(true);
    } catch (err) {
      statusError.value = messageOf(problemOf(err) ?? err);
      status.finish(false);
    }
  };

  /** Meaning mode searches one account, closest first, in a single page of at most 50. */
  const loadMeaning = async (): Promise<void> => {
    list.start();
    try {
      const res = await semanticSearch({
        accountId: accountId.value,
        q: q.value.trim(),
        limit: Math.min(ipp.value, SEMANTIC_MAX_LIMIT),
        from: from.value || undefined,
        to: to.value || undefined,
      });
      items.value = (res.data ?? []).map(semanticRow);
      total.value = items.value.length;
      totalCapped.value = false;
      list.finish(true);
      await ensureAccountLabels([accountId.value]);
    } catch (err) {
      items.value = [];
      total.value = 0;
      loadError.value = messageOf(problemOf(err) ?? err);
      list.finish(false);
      // The switch may have changed meanwhile: refresh what the toggle shows.
      if (problemCodeOf(err) === "feature_disabled") void semantic.check(accountId.value);
    }
  };

  const load = async (): Promise<void> => {
    syncUrl();
    loadError.value = null;
    const meaningWithoutAccount = mode.value === "meaning" && !accountId.value;
    if (!hasQuery.value || meaningWithoutAccount) {
      items.value = [];
      total.value = 0;
      totalCapped.value = false;
      return;
    }
    if (mode.value === "meaning") {
      await loadMeaning();
      return;
    }
    list.start();
    try {
      const res = await searchMessages({
        q: q.value.trim(),
        accountId: accountId.value || undefined,
        from: from.value || undefined,
        to: to.value || undefined,
        page: page.value,
        limit: ipp.value,
      });
      items.value = res.data ?? [];
      total.value = res.total ?? items.value.length;
      totalCapped.value = res.totalCapped === true;
      list.finish(true);
      await ensureAccountLabels(items.value.map((h) => h.accountId));
    } catch (err) {
      if (problemCodeOf(err) === "feature_disabled") {
        enabled.value = false;
        items.value = [];
        list.finish(false);
        return;
      }
      loadError.value = messageOf(problemOf(err) ?? err);
      list.finish(false);
    }
  };

  /** A new query or filter starts again at page 1. */
  const setQuery = (value: string): void => {
    q.value = value ?? "";
    page.value = 1;
    void load();
  };

  const onFilterChange = (): void => {
    page.value = 1;
    void load();
  };

  const clearFilters = (): void => {
    accountId.value = "";
    from.value = "";
    to.value = "";
    onFilterChange();
  };

  const loadAccounts = async (search = ""): Promise<void> => {
    accountsLoading.value = true;
    try {
      const rows = await searchAccounts(search);
      rememberAccounts(rows);
      accountOptions.value = rows;
    } catch (err) {
      accountOptions.value = [];
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
    } finally {
      accountsLoading.value = false;
    }
  };

  /** Switches between full-text and semantic search (a new mode starts again at page 1). */
  const setMode = (next: SearchMode): void => {
    if (next === mode.value) return;
    if (next === "meaning" && !semantic.available.value) return;
    mode.value = next;
    page.value = 1;
    void load();
  };

  // Another account: re-check semantic search; meaning mode falls back to text where it is not available.
  watch(accountId, async (id) => {
    await semantic.check(id);
    if (mode.value === "meaning" && !semantic.available.value) {
      mode.value = "text";
      page.value = 1;
      void load();
    }
  });

  const init = async (): Promise<void> => {
    await checkEnabled();
    if (enabled.value) {
      void loadAccounts();
      if (accountId.value) await semantic.check(accountId.value);
      if (mode.value === "meaning" && !semantic.available.value) mode.value = "text";
      await load();
    }
  };

  watch([page, ipp], () => {
    if (enabled.value) void load();
  });

  return {
    enabled,
    statusError,
    isCheckingSwitch: status.isFirstLoad,
    isLoading: list.isLoading,
    q,
    accountId,
    from,
    to,
    page,
    ipp,
    items,
    total,
    totalCapped,
    loadError,
    accountOptions,
    accountsLoading,
    canManageSwitches,
    hasQuery,
    hasFilters,
    init,
    load,
    setQuery,
    onFilterChange,
    clearFilters,
    loadAccounts,
    checkEnabled,
    mode,
    setMode,
    semanticStatus: semantic.status,
    semanticAvailable: semantic.available,
    semanticChecking: semantic.checking,
    semanticReasonKey: semantic.reasonKey,
  };
};
