import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "me",
    name: "profile",
    component: () => import("@/features/me/pages/ProfilePage.vue"),
    meta: { titleKey: "me.title", requiresAuth: true },
  },
];

export default routes;
