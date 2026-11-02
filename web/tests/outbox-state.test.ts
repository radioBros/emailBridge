import { describe, expect, it } from "vitest";
import {
  canCancelOutbound,
  formatAttempts,
  formatRecipients,
  isOutboundState,
  outboundStatusOf,
  OUTCOME_UNKNOWN_PLAIN,
  toApiDate,
} from "@/features/outbox/helpers/outboundState";

describe("outboundState", () => {
  it("recognises outbound states", () => {
    expect(isOutboundState("queued")).toBe(true);
    expect(isOutboundState("outcomeUnknown")).toBe(true);
    expect(isOutboundState("nope")).toBe(false);
  });

  it("allows cancel only for queued and scheduled", () => {
    expect(canCancelOutbound("queued")).toBe(true);
    expect(canCancelOutbound("scheduled")).toBe(true);
    expect(canCancelOutbound("sending")).toBe(false);
    expect(canCancelOutbound("sent")).toBe(false);
    expect(canCancelOutbound("outcomeUnknown")).toBe(false);
  });

  it("maps status colours", () => {
    expect(outboundStatusOf("sent").color).toBe("success");
    expect(outboundStatusOf("outcomeUnknown").color).toBe("warning");
    expect(outboundStatusOf("unknown-state").color).toBe("error");
  });

  it("formats recipients and attempts", () => {
    expect(formatRecipients(undefined)).toBe("—");
    expect(formatRecipients(["a@test"])).toBe("a@test");
    expect(formatRecipients(["a@test", "b@test"])).toBe("a@test +1");
    expect(formatAttempts(2, 5)).toBe("2/5");
    expect(formatAttempts(undefined, 3)).toBe("0/3");
  });

  it("explains outcomeUnknown in plain language", () => {
    expect(OUTCOME_UNKNOWN_PLAIN.toLowerCase()).toContain("mail server");
    expect(OUTCOME_UNKNOWN_PLAIN.toLowerCase()).toContain("at most once");
  });

  it("converts datetime-local to ISO", () => {
    const iso = toApiDate("2026-10-26T12:00");
    expect(iso).toMatch(/^\d{4}-\d{2}-\d{2}T/);
    expect(toApiDate("")).toBeUndefined();
  });
});
