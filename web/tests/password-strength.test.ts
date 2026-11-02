import { describe, expect, it } from "vitest";
import {
  passwordStrengthColor,
  passwordStrengthOf,
} from "@/features/auth/composables/passwordStrength";

describe("passwordStrengthOf", () => {
  it("classifies length and variety", () => {
    expect(passwordStrengthOf("")).toBe("empty");
    expect(passwordStrengthOf("short")).toBe("too_short");
    expect(passwordStrengthOf("abcdefghijkl")).toBe("weak");
    expect(passwordStrengthOf("Abcdefghijkl1")).toBe("ok");
    expect(passwordStrengthOf("Abcdefghijkl1!plusmore")).toBe("strong");
  });

  it("maps colours by meaning", () => {
    expect(passwordStrengthColor("strong")).toBe("success");
    expect(passwordStrengthColor("ok")).toBe("info");
    expect(passwordStrengthColor("weak")).toBe("warning");
  });
});
