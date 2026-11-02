import { reactive } from "vue";
import { searchAccounts, type AccountView } from "@/api/outbox";

export interface AccountLabel {
  id: string;
  name: string;
  email: string;
}

const cache = reactive<Record<string, AccountLabel>>({});

export const accountLabelOf = (id: string): AccountLabel =>
  cache[id] ?? { id, name: id, email: "" };

export const rememberAccounts = (rows: AccountView[]): void => {
  for (const row of rows) {
    cache[row.id] = { id: row.id, name: row.name, email: row.email };
  }
};

/** Resolve account names for IDs missing from the cache (one list call). */
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
