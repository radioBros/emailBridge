import { describe, expect, it } from "vitest";
import {
  hintDetailKey,
  hintTitleKey,
  severityColor,
  severityIcon,
  severityLabelKey,
  sortParam,
} from "@/features/health/helpers/severity";

describe("health severity helpers", () => {
  it("maps colours and icons", () => {
    expect(severityColor("critical")).toBe("error");
    expect(severityColor("warning")).toBe("warning");
    expect(severityColor("info")).toBe("info");
    expect(severityColor("ok")).toBe("success");
    expect(severityColor("x")).toBe("grey");
    expect(severityIcon("critical")).toBeTruthy();
    expect(severityIcon("warning")).toBeTruthy();
    expect(severityIcon("info")).toBeTruthy();
    expect(severityIcon("ok")).toBeTruthy();
    expect(severityIcon("x")).toBeTruthy();
  });

  it("builds i18n keys and sort", () => {
    expect(severityLabelKey("ok")).toBe("health.severity_ok");
    expect(hintTitleKey("app_password_required")).toContain("app_password_required");
    expect(hintDetailKey("mailbox_full")).toContain("mailbox_full");
    expect(sortParam("name", "asc")).toBe("name");
    expect(sortParam("severity", "desc")).toBe("-severity");
    expect(sortParam("nope", "asc")).toBe("-severity");
  });
});
