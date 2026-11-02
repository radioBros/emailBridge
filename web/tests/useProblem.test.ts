import { beforeEach, describe, expect, it, vi } from "vitest";
import { createI18n } from "vue-i18n";
import { ApiProblemError } from "@/api/client";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: {
    en: {
      problems: {
        internal_error: "Internal error",
        validation_failed: "Validation failed",
      },
    },
  },
});

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({
      t: i18n.global.t,
      te: i18n.global.te,
    }),
  };
});

describe("useProblem", () => {
  beforeEach(() => {
    i18n.global.locale.value = "en";
  });

  it("maps known codes and falls back to detail", async () => {
    const { useProblem } = await import("@/composables/useProblem");
    const { messageOf, fieldErrors } = useProblem();
    expect(messageOf({ status: 422, code: "validation_failed" })).toBe("Validation failed");
    expect(
      messageOf(new ApiProblemError({ status: 500, code: "unknown_x", detail: "boom" })),
    ).toBe("boom");
    expect(
      fieldErrors({
        status: 422,
        code: "validation_failed",
        errors: [{ path: "email", message: "bad" }],
      }),
    ).toEqual({ email: "bad" });
  });
});
