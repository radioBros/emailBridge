import { createRouter, createWebHistory, type RouteRecordRaw } from "vue-router";
import AdminLayout from "@/app/layouts/AdminLayout.vue";
import AuthLayout from "@/app/layouts/AuthLayout.vue";
import { resolveAuthNavigation } from "@/features/auth/composables/authGuards";
import { useSessionStore } from "@/stores/session";

const featureModules = import.meta.glob<{ default: RouteRecordRaw[] }>(
  "../features/*/routes.ts",
  { eager: true },
);

const featureRoutes: RouteRecordRaw[] = Object.values(featureModules).flatMap(
  (mod) => mod.default ?? [],
);

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/login",
      component: AuthLayout,
      meta: { public: true },
      children: [
        {
          path: "",
          name: "login",
          component: () => import("@/features/auth/pages/LoginPage.vue"),
          meta: { titleKey: "shell.login", public: true },
        },
      ],
    },
    {
      path: "/accept-invite",
      component: AuthLayout,
      meta: { public: true },
      children: [
        {
          path: "",
          name: "accept-invite",
          component: () => import("@/features/auth/pages/AcceptInvitePage.vue"),
          meta: { titleKey: "auth.invite_title", public: true },
        },
      ],
    },
    {
      path: "/connect/:token",
      component: AuthLayout,
      meta: { public: true },
      children: [
        {
          path: "",
          name: "connect",
          component: () => import("@/features/connect/pages/ConnectPage.vue"),
          meta: { titleKey: "connect.title", public: true },
        },
      ],
    },
    {
      path: "/enrolment",
      component: AuthLayout,
      children: [
        {
          path: "",
          name: "enrolment",
          component: () => import("@/features/auth/pages/EnrolmentPage.vue"),
          meta: { titleKey: "auth.enrol_title", requiresAuth: true, enrolment: true },
        },
      ],
    },
    {
      path: "/",
      component: AdminLayout,
      meta: { requiresAuth: true },
      children: featureRoutes,
    },
    {
      path: "/:pathMatch(.*)*",
      redirect: "/",
    },
  ],
});

let bootstrapped = false;

router.beforeEach(async (to) => {
  const session = useSessionStore();

  if (!bootstrapped) {
    bootstrapped = true;
    try {
      await authWait(session);
    } catch {
      /* ignore */
    }
  } else if (session.isPending) {
    await authWait(session);
  }

  if (session.isAuthenticated && (!session.enrolmentChecked || session.meLoading)) {
    if (!session.meLoading) await session.loadMe();
    else {
      await new Promise<void>((resolve) => {
        const start = Date.now();
        const tick = setInterval(() => {
          if (!session.meLoading || Date.now() - start > 5000) {
            clearInterval(tick);
            resolve();
          }
        }, 20);
      });
    }
  }

  return resolveAuthNavigation(
    {
      name: to.name,
      fullPath: to.fullPath,
      meta: {
        public: Boolean(to.meta.public),
        enrolment: Boolean(to.meta.enrolment),
      },
      query: to.query as Record<string, unknown>,
    },
    {
      isAuthenticated: session.isAuthenticated,
      needsEnrolment: session.needsEnrolment,
      enrolmentChecked: session.enrolmentChecked,
    },
  );
});

const authWait = (session: ReturnType<typeof useSessionStore>): Promise<void> =>
  new Promise((resolve) => {
    if (!session.isPending) {
      resolve();
      return;
    }
    const stop = setInterval(() => {
      if (!session.isPending) {
        clearInterval(stop);
        resolve();
      }
    }, 30);
    setTimeout(() => {
      clearInterval(stop);
      resolve();
    }, 4000);
  });

export default router;
