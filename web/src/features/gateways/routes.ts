import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "gateways",
    name: "gateways",
    component: () => import("@/features/gateways/pages/GatewaysPage.vue"),
    meta: { titleKey: "gateways.title", requiresAuth: true },
  },
];

export default routes;
