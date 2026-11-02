import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "routes",
    name: "routes",
    component: () => import("@/features/routes/pages/RoutesPage.vue"),
    meta: { titleKey: "routes.title", requiresAuth: true },
  },
];

export default routes;
