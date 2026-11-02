import { describe, expect, it } from "vitest";
import { resolveAuthNavigation } from "@/features/auth/composables/authGuards";

describe("resolveAuthNavigation", () => {
  it("sends anonymous users to login with redirect", () => {
    expect(
      resolveAuthNavigation(
        { name: "dashboard", fullPath: "/accounts", meta: {}, query: {} },
        { isAuthenticated: false, needsEnrolment: false },
      ),
    ).toEqual({ name: "login", query: { redirect: "/accounts" } });
  });

  it("allows public routes while anonymous", () => {
    expect(
      resolveAuthNavigation(
        { name: "login", fullPath: "/login", meta: { public: true }, query: {} },
        { isAuthenticated: false, needsEnrolment: false },
      ),
    ).toBe(true);
  });

  it("forces enrolment when required", () => {
    expect(
      resolveAuthNavigation(
        { name: "dashboard", fullPath: "/", meta: {}, query: {} },
        { isAuthenticated: true, needsEnrolment: true },
      ),
    ).toEqual({ name: "enrolment" });
  });

  it("allows the enrolment screen while enrolment is pending", () => {
    expect(
      resolveAuthNavigation(
        {
          name: "enrolment",
          fullPath: "/enrolment",
          meta: { enrolment: true },
          query: {},
        },
        { isAuthenticated: true, needsEnrolment: true },
      ),
    ).toBe(true);
  });

  it("bounces signed-in users away from login", () => {
    expect(
      resolveAuthNavigation(
        {
          name: "login",
          fullPath: "/login",
          meta: { public: true },
          query: { redirect: "/me" },
        },
        { isAuthenticated: true, needsEnrolment: false },
      ),
    ).toBe("/me");
  });

  it("leaves enrolment once MFA is done", () => {
    expect(
      resolveAuthNavigation(
        {
          name: "enrolment",
          fullPath: "/enrolment",
          meta: { enrolment: true },
          query: {},
        },
        { isAuthenticated: true, needsEnrolment: false, enrolmentChecked: true },
      ),
    ).toBe("/");
  });

  it("keeps enrolment while the check is still pending", () => {
    expect(
      resolveAuthNavigation(
        {
          name: "enrolment",
          fullPath: "/enrolment",
          meta: { enrolment: true },
          query: {},
        },
        { isAuthenticated: true, needsEnrolment: false, enrolmentChecked: false },
      ),
    ).toBe(true);
  });
});
