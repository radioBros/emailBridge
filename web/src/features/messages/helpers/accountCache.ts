import type { AccountView } from "@/api/messages";

const cache = new Map<string, { name: string; email: string }>();

export const rememberAccounts = (accounts: AccountView[]): void => {
  for (const a of accounts) {
    cache.set(a.id, { name: a.name, email: a.email });
  }
};

export const accountLabelOf = (accountId: string): { name: string; email: string } =>
  cache.get(accountId) ?? { name: accountId, email: "" };

export const ensureAccountLabels = async (
  ids: string[],
  fetchMissing: (q: string) => Promise<AccountView[]>,
): Promise<void> => {
  const missing = ids.filter((id) => id && !cache.has(id));
  if (!missing.length) return;
  const all = await fetchMissing("");
  rememberAccounts(all);
};
