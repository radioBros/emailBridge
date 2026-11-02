import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "search",
    name: "search",
    component: () => import("@/features/search/pages/SearchPage.vue"),
    meta: { titleKey: "search.title", requiresAuth: true },
  },
];

export default routes;
