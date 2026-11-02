import { describe, expect, it } from "vitest";
import { ApiProblemError } from "@/api/client";
import { AuthProblemError } from "@/api/authFlow";
import { isProblemCode, problemCodeOf, problemOf } from "@/api/problems";
import { LimenError } from "limen-auth";

describe("problems helpers", () => {
  it("reads AuthProblemError and ApiProblemError", () => {
    const auth = new AuthProblemError({ status: 401, code: "mfa_invalid", detail: "x" }, 5);
    expect(problemOf(auth)?.code).toBe("mfa_invalid");
    expect(problemCodeOf(auth)).toBe("mfa_invalid");
    expect(isProblemCode(auth, "mfa_invalid")).toBe(true);

    const api = new ApiProblemError({ status: 422, code: "validation_failed" });
    expect(problemOf(api)?.code).toBe("validation_failed");
  });

  it("maps LimenError status to a problem code", () => {
    expect(problemOf(new LimenError("nope", 401))?.code).toBe("invalid_credentials");
    expect(problemOf(new LimenError("locked", 423))?.code).toBe("account_locked");
    expect(problemOf(new LimenError("slow", 429))?.code).toBe("rate_limited");
    expect(problemOf("plain")).toBeNull();
  });
});
