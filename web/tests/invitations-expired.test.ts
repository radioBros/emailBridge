import { describe, expect, it } from "vitest";
import { isExpiredInviteCode } from "@/api/invitations";

describe("isExpiredInviteCode", () => {
  it("recognises the invitation_expired problem code", () => {
    expect(isExpiredInviteCode("invitation_expired")).toBe(true);
    expect(isExpiredInviteCode("invite_expired")).toBe(true);
    expect(isExpiredInviteCode("gone")).toBe(true);
    expect(isExpiredInviteCode("not_found")).toBe(false);
    expect(isExpiredInviteCode("validation_failed")).toBe(false);
  });
});
