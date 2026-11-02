import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "health/accounts",
    name: "health-accounts",
    component: () => import("@/features/health/pages/OrgHealthPage.vue"),
    meta: { titleKey: "health.title", requiresAuth: true },
  },
];

export default routes;
