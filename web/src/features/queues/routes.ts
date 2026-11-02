import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "queues",
    name: "queues",
    component: () => import("@/features/queues/pages/QueuesPage.vue"),
    meta: { titleKey: "queues.title", requiresAuth: true },
  },
];

export default routes;
