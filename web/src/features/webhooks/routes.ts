import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "webhooks",
    name: "webhooks",
    component: () => import("@/features/webhooks/pages/WebhooksPage.vue"),
    meta: { titleKey: "webhooks.title", requiresAuth: true },
  },
];

export default routes;
