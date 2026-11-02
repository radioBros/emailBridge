import { describe, expect, it } from "vitest";
import {
  DEFAULT_SAMPLE_VARS,
  extractHbsVariables,
  parseSampleVariables,
} from "@/features/templates/helpers/hbsHints";

describe("hbsHints", () => {
  it("extracts root variable names", () => {
    const vars = extractHbsVariables(
      "Hello {{name}}",
      "{{{html.body}}}",
      "{{#if flag}}x{{/if}}",
      "{{> footer}}",
    );
    expect(vars).toEqual(["html", "name"]);
  });

  it("parses sample JSON object", () => {
    expect(parseSampleVariables(DEFAULT_SAMPLE_VARS).ok).toBe(true);
    expect(parseSampleVariables("[]").ok).toBe(false);
    expect(parseSampleVariables("{").ok).toBe(false);
    expect(parseSampleVariables("").ok).toBe(true);
  });
});
