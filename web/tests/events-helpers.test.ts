import { describe, expect, it } from "vitest";
import { familyColor, familyOf, toApiDate } from "@/features/events/helpers/types";
import { eventsStreamPath } from "@/api/events";

describe("events helpers", () => {
  it("maps families and colours", () => {
    expect(familyOf("message.received")).toBe("message");
    expect(familyOf("account.added")).toBe("account");
    expect(familyOf("weird")).toBe("other");
    expect(familyColor("message.sent")).toBe("info");
    expect(familyColor("endpoint.test")).toBe("warning");
    expect(familyColor("x")).toBe("grey");
  });

  it("toApiDate", () => {
    expect(toApiDate("")).toBeUndefined();
    expect(toApiDate("2026-10-26T10:00:00.000Z")).toBe("2026-10-26T10:00:00.000Z");
  });

  it("eventsStreamPath", () => {
    expect(eventsStreamPath({})).toBe("/v1/events/stream");
    expect(eventsStreamPath({ type: "message.*", accountId: "acc_1", lastEventId: "evt_1" })).toBe(
      "/v1/events/stream?type=message.*&accountId=acc_1&lastEventId=evt_1",
    );
  });
});
