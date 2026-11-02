import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "members",
    name: "members",
    component: () => import("@/features/organization/pages/MembersPage.vue"),
    meta: { titleKey: "organization.members.title", requiresAuth: true },
  },
  {
    path: "api-keys",
    name: "api-keys",
    component: () => import("@/features/organization/pages/ApiKeysPage.vue"),
    meta: { titleKey: "organization.api_keys.title", requiresAuth: true },
  },
  {
    path: "audit",
    name: "audit",
    component: () => import("@/features/organization/pages/AuditPage.vue"),
    meta: { titleKey: "organization.audit.title", requiresAuth: true },
  },
];

export default routes;
