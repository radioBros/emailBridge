import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "events",
    name: "events",
    component: () => import("@/features/events/pages/EventsPage.vue"),
    meta: { titleKey: "events.title", requiresAuth: true },
  },
];

export default routes;
