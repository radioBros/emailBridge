import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "notifications",
    name: "notifications",
    component: () => import("@/features/notifications/pages/NotificationsPage.vue"),
    meta: { titleKey: "notifications.title", requiresAuth: true },
  },
];

export default routes;
