import type { RouteRecordRaw } from "vue-router";

/** Public `/connect/:token` is mounted top-level in `app/router.ts` (AuthLayout). */
const routes: RouteRecordRaw[] = [
  {
    path: "branding",
    name: "branding",
    component: () => import("@/features/connect/pages/BrandingPage.vue"),
    meta: { titleKey: "connect.branding.title", requiresAuth: true },
  },
];

export default routes;
