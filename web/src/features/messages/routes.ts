import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "messages",
    name: "messages",
    component: () => import("@/features/messages/pages/MessagesPage.vue"),
    meta: { titleKey: "messages.title", requiresAuth: true },
  },
];

export default routes;
