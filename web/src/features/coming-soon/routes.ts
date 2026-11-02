import type { RouteRecordRaw } from "vue-router";
import { navigation } from "@/app/navigation";

const ComingSoon = () => import("@/features/coming-soon/pages/ComingSoonPage.vue");

const routes: RouteRecordRaw[] = navigation
  .flatMap((g) => g.items)
  .filter((item) => item.comingSoon)
  .map((item) => ({
    path: item.to.replace(/^\//, ""),
    name: item.to.slice(1).replace(/\//g, "-"),
    component: ComingSoon,
    meta: { titleKey: item.titleKey, comingSoon: true },
  }));

export default routes;
