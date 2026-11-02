import type { RouteRecordRaw } from "vue-router";

const routes: RouteRecordRaw[] = [
  {
    path: "templates",
    name: "templates",
    component: () => import("@/features/templates/pages/TemplatesPage.vue"),
    meta: { titleKey: "templates.title", requiresAuth: true },
  },
  {
    path: "templates/new",
    name: "templates-new",
    component: () => import("@/features/templates/pages/TemplateEditorPage.vue"),
    meta: { titleKey: "templates.editor_new", requiresAuth: true },
  },
  {
    path: "templates/:templateId",
    name: "templates-edit",
    component: () => import("@/features/templates/pages/TemplateEditorPage.vue"),
    meta: { titleKey: "templates.editor_edit", requiresAuth: true },
  },
];

export default routes;
