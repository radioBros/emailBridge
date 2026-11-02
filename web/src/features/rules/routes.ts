import type { RouteRecordRaw } from "vue-router";

/** W73b: the "Filter & transform" editor for endpoints and routes, and the named lists screen. */
const routes: RouteRecordRaw[] = [
  {
    path: "webhooks/:endpointId/filter",
    name: "webhook-rule",
    component: () => import("@/features/rules/pages/EndpointRulePage.vue"),
    meta: { titleKey: "rules.page_title", requiresAuth: true },
  },
  {
    path: "routes/:routeId/filter",
    name: "route-rule",
    component: () => import("@/features/rules/pages/RouteRulePage.vue"),
    meta: { titleKey: "rules.page_title", requiresAuth: true },
  },
  {
    path: "rule-lists",
    name: "rule-lists",
    component: () => import("@/features/rules/pages/RuleListsPage.vue"),
    meta: { titleKey: "rules.lists.title", requiresAuth: true },
  },
];

export default routes;
