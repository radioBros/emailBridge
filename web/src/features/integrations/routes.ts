import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "integrations",
    name: "integrations",
    component: () => import("@/features/integrations/pages/IntegrationsPage.vue"),
    meta: { titleKey: "integrations.title", requiresAuth: true },
  },
];

export default routes;
