import { reactive } from "vue";
import { searchAccounts, type AccountView } from "@/api/outbox";

const accounts = reactive<Record<string, { name: string; email: string }>>({});

export const accountLabelOf = (id: string): string => accounts[id]?.name || id;

export const rememberAccounts = (rows: AccountView[]): void => {
  for (const row of rows) {
    accounts[row.id] = { name: row.name, email: row.email ?? "" };
  }
};

export const ensureAccountLabels = async (ids: string[]): Promise<void> => {
  const missing = [...new Set(ids.filter((id) => id && !accounts[id]))];
  if (!missing.length) return;
  try {
    rememberAccounts(await searchAccounts(""));
  } catch {
    /* keep IDs */
  }
};
