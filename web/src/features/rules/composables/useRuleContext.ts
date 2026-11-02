import { inject, provide, reactive, ref, type InjectionKey, type Ref } from "vue";
import { getAccount, type AccountView } from "@/api/accounts";
import { listMailboxes, searchAccounts, type MailboxView } from "@/api/messages";
import { listRuleLists, type RuleList } from "@/api/rules";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";

/**
 * Lookups the builder rows share: account names, each account's real mailboxes, named lists, the event
 * types the rule is about and the secret names it knows. Provided once by `RuleEditor`, cached for the
 * page, so ten folder rows cost one mailbox request.
 */
export interface RuleContext {
  eventTypes: Ref<string[]>;
  secretNames: Ref<string[]>;
  /** Account the folder / label pickers list (from the rule, the endpoint filter or the user's pick). */
  contextAccountId: Ref<string>;
  accounts: Record<string, AccountView>;
  accountLabel: (id: string) => string;
  ensureAccount: (id: string) => Promise<void>;
  searchAccounts: (q: string) => Promise<AccountView[]>;
  mailboxes: (accountId: string) => Promise<MailboxView[]>;
  isGmail: (accountId: string) => boolean;
  lists: Ref<RuleList[]>;
  loadLists: () => Promise<void>;
}

const KEY: InjectionKey<RuleContext> = Symbol("ruleContext");

export const createRuleContext = (): RuleContext => {
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const accounts = reactive<Record<string, AccountView>>({});
  const mailboxCache = new Map<string, Promise<MailboxView[]>>();
  const pendingAccounts = new Map<string, Promise<void>>();
  const lists = ref<RuleList[]>([]);
  let listsLoaded: Promise<void> | null = null;

  const fail = (e: unknown): void => {
    notify.push({ message: messageOf(e), color: "error" });
  };

  const remember = (rows: AccountView[]): void => {
    for (const a of rows) accounts[a.id] = a;
  };

  const ensureAccount = (id: string): Promise<void> => {
    if (!id || accounts[id]) return Promise.resolve();
    const pending = pendingAccounts.get(id);
    if (pending) return pending;
    const p = getAccount(id)
      .then((a) => {
        accounts[a.id] = a as AccountView;
      })
      .catch(() => undefined); // a deleted account keeps showing its ID
    pendingAccounts.set(id, p);
    return p;
  };

  const ctx: RuleContext = {
    eventTypes: ref<string[]>([]),
    secretNames: ref<string[]>([]),
    contextAccountId: ref(""),
    accounts,
    accountLabel: (id: string): string => {
      const a = accounts[id];
      if (!a) return id;
      return a.name && a.name !== a.email ? `${a.name} · ${a.email}` : a.email || a.name || id;
    },
    ensureAccount,
    searchAccounts: async (q: string): Promise<AccountView[]> => {
      try {
        const rows = (await searchAccounts(q)) as AccountView[];
        remember(rows);
        return rows;
      } catch (e) {
        fail(e);
        return [];
      }
    },
    mailboxes: (accountId: string): Promise<MailboxView[]> => {
      if (!accountId) return Promise.resolve([]);
      let p = mailboxCache.get(accountId);
      if (!p) {
        p = listMailboxes(accountId)
          .then((r) => r.data ?? [])
          .catch((e: unknown) => {
            mailboxCache.delete(accountId);
            fail(e);
            return [];
          });
        mailboxCache.set(accountId, p);
      }
      return p;
    },
    isGmail: (accountId: string): boolean => {
      const a = accounts[accountId];
      if (!a) return false;
      const host = (a.imap as { host?: string } | undefined)?.host ?? "";
      return a.provider === "gmail" || /gmail\.com$|googlemail\.com$/i.test(host) || /@gmail\.com$/i.test(a.email);
    },
    lists,
    loadLists: (): Promise<void> => {
      if (!listsLoaded) {
        listsLoaded = listRuleLists({ limit: 100, sort: "name" })
          .then((r) => {
            lists.value = r.data ?? [];
          })
          .catch((e: unknown) => {
            listsLoaded = null;
            fail(e);
          });
      }
      return listsLoaded;
    },
  };
  return ctx;
};

export const provideRuleContext = (ctx: RuleContext): void => provide(KEY, ctx);

export const useRuleContext = (): RuleContext => {
  const ctx = inject(KEY, null);
  if (!ctx) throw new Error("useRuleContext outside RuleEditor");
  return ctx;
};
