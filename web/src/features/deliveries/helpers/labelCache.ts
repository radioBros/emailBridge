import { reactive } from "vue";
import { listWebhookEndpoints } from "@/api/webhooks";
import { listRoutes } from "@/api/routes";
import { searchAccounts, type AccountView } from "@/api/outbox";

const accounts = reactive<Record<string, { name: string; email: string }>>({});
const endpoints = reactive<Record<string, string>>({});
const routes = reactive<Record<string, string>>({});

export const accountLabelOf = (id: string): string => accounts[id]?.name || id;

export const endpointLabelOf = (id: string): string => endpoints[id] || id;

export const routeLabelOf = (id: string): string => routes[id] || id;

export const rememberAccounts = (rows: AccountView[]): void => {
  for (const row of rows) {
    accounts[row.id] = { name: row.name, email: row.email ?? "" };
  }
};

export const ensureLabels = async (opts: {
  accountIds: string[];
  endpointIds: string[];
  routeIds: string[];
}): Promise<void> => {
  const missingAcc = [...new Set(opts.accountIds.filter((id) => id && !accounts[id]))];
  const missingEp = [...new Set(opts.endpointIds.filter((id) => id && !endpoints[id]))];
  const missingRt = [...new Set(opts.routeIds.filter((id) => id && !routes[id]))];
  try {
    if (missingAcc.length) {
      const rows = await searchAccounts("");
      rememberAccounts(rows);
    }
  } catch {
    /* keep IDs */
  }
  try {
    if (missingEp.length) {
      const res = await listWebhookEndpoints({ page: 1, limit: 200, includeTotal: false });
      for (const row of res.data ?? []) {
        endpoints[row.id] = row.name;
      }
    }
  } catch {
    /* keep IDs */
  }
  try {
    if (missingRt.length) {
      const res = await listRoutes({ page: 1, limit: 200, includeTotal: false });
      for (const row of res.data ?? []) {
        routes[row.id] = row.name;
      }
    }
  } catch {
    /* keep IDs */
  }
};
