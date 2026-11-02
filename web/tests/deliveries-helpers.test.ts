import { describe, expect, it } from "vitest";
import {
  DELIVERY_STATE_KEYS,
  FAILED_QUICK,
  deliveryStatusOf,
  isDeliveryState,
  lastStatusCode,
  toApiDate,
} from "@/features/deliveries/helpers/states";
import { jobProgressText } from "@/features/deliveries/helpers/jobPoll";

describe("deliveries helpers", () => {
  it("exposes seven delivery states", () => {
    expect(DELIVERY_STATE_KEYS).toHaveLength(7);
    expect(isDeliveryState("failed")).toBe(true);
    expect(isDeliveryState("nope")).toBe(false);
    expect(deliveryStatusOf("succeeded").color).toBe("success");
    expect(deliveryStatusOf("weird").color).toBe("error");
    expect(FAILED_QUICK).toContain("deadLettered");
  });

  it("reads last status and dates", () => {
    expect(lastStatusCode([])).toBeNull();
    expect(lastStatusCode([{ statusCode: 204 }, { statusCode: 500 }])).toBe(500);
    expect(toApiDate("")).toBeUndefined();
    expect(toApiDate("2026-10-26T00:00:00.000Z")).toMatch(/2026/);
  });

  it("formats job progress text", () => {
    expect(
      jobProgressText({
        id: "j",
        kind: "k",
        state: "queued",
        createdAt: "t",
      }),
    ).toBe("queued");
  });
});
