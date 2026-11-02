import { describe, expect, it } from "vitest";
import {
  baseScopesOf,
  granularScope,
  parseGranularScopes,
  API_KEY_SCOPES,
  scopeItems,
} from "@/features/organization/helpers/scopes";

describe("api key scopes helpers", () => {
  it("lists org-wide scopes including admin", () => {
    expect(API_KEY_SCOPES).toContain("admin");
    expect(API_KEY_SCOPES).toContain("accounts:write");
    expect(API_KEY_SCOPES[1]).toBe("mcp:use");
    expect(scopeItems((s) => (s === "mcp:use" ? "MCP" : undefined))[1]).toEqual({ title: "MCP", value: "mcp:use" });
    expect(API_KEY_SCOPES.some((s) => s.split(":").length === 3)).toBe(false);
  });

  it("builds and parses granular scopes", () => {
    const s = granularScope("accounts", "write", "acc_1");
    expect(s).toBe("accounts:write:acc_1");
    expect(parseGranularScopes(["admin", s, "webhooks:read:whe_1"])).toEqual([
      { resource: "accounts", mode: "write", id: "acc_1" },
      { resource: "webhooks", mode: "read", id: "whe_1" },
    ]);
  });

  it("filters base scopes", () => {
    expect(baseScopesOf(["admin", "accounts:read", "accounts:write:acc_1"])).toEqual([
      "admin",
      "accounts:read",
    ]);
  });
});
