import { describe, expect, it } from "vitest";
import {
  canCancelExport,
  canDownloadExport,
  exportStatusOf,
  formatBytes,
  progressPct,
} from "@/features/exports/helpers/states";

describe("exports states helpers", () => {
  it("maps known states", () => {
    expect(exportStatusOf("succeeded").color).toBe("success");
    expect(exportStatusOf("running").color).toBe("info");
    expect(exportStatusOf("weird").color).toBe("grey");
  });

  it("gates cancel and download", () => {
    expect(canCancelExport("queued")).toBe(true);
    expect(canCancelExport("running")).toBe(true);
    expect(canCancelExport("succeeded")).toBe(false);
    expect(canDownloadExport("succeeded")).toBe(true);
    expect(canDownloadExport("running")).toBe(false);
  });

  it("formats bytes and progress", () => {
    expect(formatBytes(undefined)).toBe("—");
    expect(formatBytes(500)).toBe("500 B");
    expect(formatBytes(2048)).toMatch(/KB/);
    expect(formatBytes(2 * 1024 * 1024)).toMatch(/MB/);
    expect(progressPct(0, 0)).toBe(0);
    expect(progressPct(25, 100)).toBe(25);
    expect(progressPct(150, 100)).toBe(100);
  });
});
