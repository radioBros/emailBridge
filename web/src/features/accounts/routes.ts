import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "accounts",
    name: "accounts",
    component: () => import("@/features/accounts/pages/AccountsListPage.vue"),
    meta: { titleKey: "accounts.title", requiresAuth: true },
  },
  {
    path: "accounts/new",
    name: "accounts-new",
    component: () => import("@/features/accounts/pages/AccountFormPage.vue"),
    meta: { titleKey: "accounts.create_title", requiresAuth: true },
  },
  {
    path: "accounts/:id",
    name: "accounts-detail",
    component: () => import("@/features/accounts/pages/AccountDetailPage.vue"),
    meta: { titleKey: "accounts.detail_title", requiresAuth: true },
  },
  {
    path: "accounts/:id/edit",
    name: "accounts-edit",
    component: () => import("@/features/accounts/pages/AccountFormPage.vue"),
    meta: { titleKey: "accounts.edit_title", requiresAuth: true },
  },
];

export default routes;
