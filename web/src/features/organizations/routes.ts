import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "organizations",
    name: "organizations",
    component: () => import("@/features/organizations/pages/OrganizationsPage.vue"),
    meta: { titleKey: "organizations.title", requiresAuth: true },
  },
];

export default routes;
