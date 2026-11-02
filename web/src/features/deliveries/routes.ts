import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "deliveries",
    name: "deliveries",
    component: () => import("@/features/deliveries/pages/DeliveriesPage.vue"),
    meta: { titleKey: "deliveries.title", requiresAuth: true },
  },
];

export default routes;
