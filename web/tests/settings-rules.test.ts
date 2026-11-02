import { describe, expect, it } from "vitest";
import {
  durationFormat,
  rateFormat,
  rulesForField,
  sizeFormat,
} from "@/features/settings/helpers/rules";

const ok = (rules: Array<(v: unknown) => true | string>, value: unknown) =>
  rules.every((r) => r(value) === true);

const fail = (rules: Array<(v: unknown) => true | string>, value: unknown) =>
  rules.some((r) => r(value) !== true);

describe("settings rules", () => {
  it("accepts Go durations and day suffix", () => {
    expect(ok(durationFormat, "30s")).toBe(true);
    expect(ok(durationFormat, "5m")).toBe(true);
    expect(ok(durationFormat, "1h30m")).toBe(true);
    expect(ok(durationFormat, "7d")).toBe(true);
    expect(fail(durationFormat, "7 days")).toBe(true);
  });

  it("accepts rates and sizes", () => {
    expect(ok(rateFormat, "20/s,burst=40")).toBe(true);
    expect(ok(rateFormat, "10/min")).toBe(true);
    expect(fail(rateFormat, "20 per second")).toBe(true);
    expect(ok(sizeFormat, "25MiB")).toBe(true);
    expect(ok(sizeFormat, "0")).toBe(true);
    expect(fail(sizeFormat, "25MB")).toBe(true);
  });

  it("skips rules when locked", () => {
    expect(rulesForField("rate", "rateApiKey", true)).toEqual([]);
  });
});
