import { describe, expect, it } from "vitest";
import { Actions } from "@/constants/Actions";
import {
  AccountStates,
  BreakerStates,
  DeliveryStates,
  LogLevels,
  OutboundStates,
} from "@/constants/Statuses";

describe("Actions / Statuses maps", () => {
  it("every action has colour and mdi path icon", () => {
    for (const [key, spec] of Object.entries(Actions)) {
      expect(spec.color, key).toBeTruthy();
      expect(spec.icon, key).toMatch(/^M/);
    }
  });

  it("covers the W78 semantic action families", () => {
    const required = [
      "create",
      "add",
      "save",
      "confirm",
      "send",
      "edit",
      "delete",
      "export",
      "download",
      "import",
      "upload",
      "refresh",
      "retry",
      "filter",
      "ids",
      "settings",
      "configure",
      "live",
      "liveActive",
      "stream",
      "test",
      "copy",
      "open",
      "navigate",
      "cancel",
      "close",
    ] as const;
    for (const key of required) {
      expect(Actions[key], key).toBeTruthy();
    }
    expect(Actions.cancel.color).toBe("secondary");
    expect(Actions.close.color).toBe("secondary");
    expect(Actions.export.color).not.toBe("error");
    expect(Actions.liveActive.color).toBe("warning");
  });

  it("status maps cover S13 domains", () => {
    expect(Object.keys(AccountStates)).toContain("connected");
    expect(Object.keys(OutboundStates)).toContain("sent");
    expect(Object.keys(DeliveryStates)).toContain("succeeded");
    expect(Object.keys(LogLevels)).toContain("error");
    expect(Object.keys(BreakerStates)).toEqual(["closed", "halfOpen", "open"]);
  });
});
