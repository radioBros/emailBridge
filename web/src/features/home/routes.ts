import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "",
    name: "dashboard",
    component: () => import("@/features/home/pages/DashboardPage.vue"),
    meta: { titleKey: "dashboard.title" },
  },
];

export default routes;
