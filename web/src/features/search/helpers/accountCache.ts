import { reactive } from "vue";
import { searchAccounts, type AccountView } from "@/api/outbox";

const accounts = reactive<Record<string, { name: string; email: string }>>({});

/** Display name of an account (its ID until the name is known). */
export const accountLabelOf = (id: string): string => accounts[id]?.name || id;

export const rememberAccounts = (rows: AccountView[]): void => {
  for (const row of rows) {
    accounts[row.id] = { name: row.name, email: row.email ?? "" };
  }
};

/** Loads the names of accounts not seen yet (best effort: IDs stay on failure). */
export const ensureAccountLabels = async (ids: string[]): Promise<void> => {
  const missing = [...new Set(ids.filter((id) => id && !accounts[id]))];
  if (!missing.length) return;
  try {
    rememberAccounts(await searchAccounts(""));
  } catch {
    /* keep IDs */
  }
};
