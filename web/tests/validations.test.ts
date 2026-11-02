import { describe, expect, it } from "vitest";
import Validations from "@/constants/Validations";

describe("Validations", () => {
  it("required and email rules", () => {
    const required = Validations.required[0];
    const email = Validations.email[0];
    expect(required("")).not.toBe(true);
    expect(required("x")).toBe(true);
    expect(email("")).toBe(true);
    expect(email("a@b.com")).toBe(true);
    expect(email("nope")).not.toBe(true);
  });
});
