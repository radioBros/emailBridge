import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "cluster",
    name: "cluster",
    component: () => import("@/features/cluster/pages/ClusterPage.vue"),
    meta: { titleKey: "cluster.title", requiresAuth: true },
  },
];

export default routes;
