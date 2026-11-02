import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "sandbox",
    name: "sandbox",
    component: () => import("@/features/sandbox/pages/SandboxPage.vue"),
    meta: { titleKey: "sandbox.title", requiresAuth: true },
  },
];

export default routes;
