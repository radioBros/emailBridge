import { describe, expect, it } from "vitest";
import {
  cursorTotal,
  formatBytes,
  formatLogTime,
  prettyJson,
} from "@/features/logs/helpers/logFormat";

describe("logFormat", () => {
  it("formats nanosecond timestamps", () => {
    // 2026-01-01T00:00:00.000Z
    const ns = 1_767_225_600_000_000_000;
    const label = formatLogTime(ns);
    expect(label).not.toBe("—");
    expect(label.length).toBeGreaterThan(10);
  });

  it("formats disk bytes", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(2048)).toMatch(/KiB/);
    expect(formatBytes(5 * 1024 * 1024)).toMatch(/MiB/);
  });

  it("pretty-prints JSON", () => {
    expect(prettyJson({ a: 1 })).toContain('"a": 1');
  });

  it("computes cursor totals for page mode", () => {
    expect(cursorTotal(1, 50, 50, true)).toBe(51);
    expect(cursorTotal(1, 50, 12, false)).toBe(12);
    expect(cursorTotal(2, 50, 50, true)).toBe(101);
  });
});
