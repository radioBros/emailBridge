import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "logs",
    name: "logs",
    component: () => import("@/features/logs/pages/LogsPage.vue"),
    meta: { titleKey: "logs.title", requiresAuth: true },
  },
];

export default routes;
