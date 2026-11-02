import { describe, expect, it } from "vitest";
import {
  assignableRoles,
  canEditMember,
  canReadAudit,
  canWriteApiKeys,
  canWriteMembers,
  isOrgRole,
  roleColor,
} from "@/features/organization/helpers/roles";
import { API_KEY_SCOPES, scopeItems } from "@/features/organization/helpers/scopes";
import { sortParam } from "@/features/organization/helpers/sort";

describe("organization roles", () => {
  it("gates write capabilities by role", () => {
    expect(canWriteMembers("owner")).toBe(true);
    expect(canWriteMembers("admin")).toBe(true);
    expect(canWriteMembers("operator")).toBe(false);
    expect(canWriteMembers("viewer")).toBe(false);

    expect(canWriteApiKeys("admin")).toBe(true);
    expect(canWriteApiKeys("operator")).toBe(false);

    expect(canReadAudit("operator")).toBe(true);
    expect(canReadAudit("viewer")).toBe(false);
  });

  it("prevents admin from editing owners", () => {
    expect(canEditMember("admin", "owner")).toBe(false);
    expect(canEditMember("admin", "viewer")).toBe(true);
    expect(canEditMember("owner", "owner")).toBe(true);
    expect(assignableRoles("admin")).not.toContain("owner");
    expect(assignableRoles("owner")).toContain("owner");
  });

  it("maps role colours and validates role strings", () => {
    expect(isOrgRole("admin")).toBe(true);
    expect(isOrgRole("nope")).toBe(false);
    expect(roleColor("owner")).toBe("primary");
    expect(roleColor("viewer")).toBe("grey");
  });
});

describe("api key scopes", () => {
  it("exposes admin plus read/write pairs", () => {
    expect(API_KEY_SCOPES).toContain("admin");
    expect(API_KEY_SCOPES).toContain("accounts:read");
    expect(API_KEY_SCOPES).toContain("accounts:write");
    expect(scopeItems().length).toBe(API_KEY_SCOPES.length);
    expect(API_KEY_SCOPES.length).toBeGreaterThan(4);
  });
});

describe("sortParam", () => {
  it("encodes desc with a leading dash", () => {
    expect(sortParam([{ key: "at", order: "desc" }])).toBe("-at");
    expect(sortParam([{ key: "email", order: "asc" }])).toBe("email");
    expect(sortParam([])).toBeUndefined();
  });
});
