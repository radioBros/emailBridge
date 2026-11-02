import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "outbox",
    name: "outbox",
    component: () => import("@/features/outbox/pages/OutboxPage.vue"),
    meta: { titleKey: "outbox.title", requiresAuth: true },
  },
];

export default routes;
