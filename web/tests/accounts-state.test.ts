import { describe, expect, it } from "vitest";
import {
  accountStatusOf,
  canDelete,
  canEdit,
  canReconnect,
  canResume,
  canSync,
  isAccountState,
  switchOn,
} from "@/features/accounts/helpers/accountState";

describe("accountState helpers", () => {
  it("maps known states and falls back", () => {
    expect(isAccountState("connected")).toBe(true);
    expect(isAccountState("nope")).toBe(false);
    expect(accountStatusOf("connected").color).toBe("success");
    expect(accountStatusOf("weird").color).toBe("warning");
  });

  it("gates actions by state", () => {
    expect(canResume("disabled")).toBe(true);
    expect(canResume("connected")).toBe(false);
    expect(canSync("disabled")).toBe(false);
    expect(canSync("connected")).toBe(true);
    expect(canReconnect("new")).toBe(false);
    expect(canReconnect("authError")).toBe(true);
    expect(canEdit("deleting")).toBe(false);
    expect(canDelete("deleting")).toBe(false);
    expect(canDelete("connected")).toBe(true);
  });

  it("reads switch flags", () => {
    expect(switchOn({ "protocolLogs.enabled": true }, "protocolLogs.enabled")).toBe(true);
    expect(switchOn({}, "protocolLogs.enabled")).toBe(false);
    expect(switchOn(undefined, "x")).toBe(false);
  });
});
