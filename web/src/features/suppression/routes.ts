import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "suppression",
    name: "suppression",
    component: () => import("@/features/suppression/pages/SuppressionPage.vue"),
    meta: { titleKey: "suppression.title", requiresAuth: true },
  },
  {
    path: "suppression/:listId",
    name: "suppression-entries",
    component: () => import("@/features/suppression/pages/SuppressionEntriesPage.vue"),
    meta: { titleKey: "suppression.title", requiresAuth: true },
  },
];

export default routes;
