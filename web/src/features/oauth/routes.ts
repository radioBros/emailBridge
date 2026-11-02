import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "oauth-apps",
    name: "oauth-apps",
    component: () => import("@/features/oauth/pages/OauthAppsPage.vue"),
    meta: { titleKey: "oauth.title", requiresAuth: true },
  },
];

export default routes;
