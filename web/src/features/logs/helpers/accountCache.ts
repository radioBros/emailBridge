import { reactive } from "vue";
import { searchAccounts, type AccountOption } from "@/api/logs";

export interface AccountLabel {
  id: string;
  name: string;
  email: string;
}

const cache = reactive<Record<string, AccountLabel>>({});

export const accountLabelOf = (id: string): AccountLabel =>
  cache[id] ?? { id, name: id, email: "" };

export const rememberAccounts = (rows: AccountOption[]): void => {
  for (const row of rows) {
    cache[row.id] = { id: row.id, name: row.name, email: row.email ?? "" };
  }
};

/** Resolve account names for IDs missing from the cache. */
export const ensureAccountLabels = async (ids: string[]): Promise<void> => {
  const missing = [...new Set(ids.filter((id) => id && !cache[id]))];
  if (!missing.length) return;
  try {
    const rows = await searchAccounts("");
    rememberAccounts(rows);
  } catch {
    /* keep IDs as fallback */
  }
};
