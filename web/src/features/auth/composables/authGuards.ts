export interface GuardSession {
  isAuthenticated: boolean;
  needsEnrolment: boolean;
  /** When false, enrolment redirects must wait (me still loading). */
  enrolmentChecked?: boolean;
}

export interface GuardRoute {
  name?: string | symbol | null;
  fullPath: string;
  meta: { public?: boolean; enrolment?: boolean };
  query: Record<string, unknown>;
}

export type GuardResult =
  | true
  | { name: string; query?: Record<string, string> }
  | string;

/**
 * Pure router-guard decision (unauthenticated → login, enrolment gate, bounce off login).
 * Kept free of Vue so Vitest can cover it without mounting the app.
 */
export const resolveAuthNavigation = (
  to: GuardRoute,
  session: GuardSession,
): GuardResult => {
  const isPublic = Boolean(to.meta.public);
  const isEnrolment = Boolean(to.meta.enrolment);

  if (!session.isAuthenticated && !isPublic) {
    return { name: "login", query: { redirect: to.fullPath } };
  }

  if (session.isAuthenticated) {
    if (session.needsEnrolment && !isEnrolment && to.name !== "login") {
      return { name: "enrolment" };
    }
    if (!session.needsEnrolment && (to.name === "login" || to.name === "accept-invite")) {
      const redirect = typeof to.query.redirect === "string" ? to.query.redirect : "/";
      return redirect.startsWith("/") ? redirect : "/";
    }
    if (
      !session.needsEnrolment &&
      isEnrolment &&
      session.enrolmentChecked !== false
    ) {
      return "/";
    }
  }

  return true;
};
