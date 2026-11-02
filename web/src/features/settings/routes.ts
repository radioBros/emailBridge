import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "settings",
    name: "settings",
    component: () => import("@/features/settings/pages/SettingsPage.vue"),
    meta: { titleKey: "settings.title", requiresAuth: true },
  },
];

export default routes;
