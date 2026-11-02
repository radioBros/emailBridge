import type { ServerTableSortItem } from "@/components/tables/types";

/** Map Vuetify sortBy → API `sort` query (`-key` for desc). */
export const sortParam = (sortBy: ServerTableSortItem[]): string | undefined => {
  const first = sortBy[0];
  if (!first?.key) return undefined;
  return first.order === "desc" ? `-${first.key}` : first.key;
};
