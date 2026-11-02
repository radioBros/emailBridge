import { describe, expect, it } from "vitest";
import {
  htmlToText,
  isEmptyHtml,
  sanitizeHtml,
} from "@/features/outbox/helpers/richText";

describe("richText helpers", () => {
  it("strips script/style and event handlers", () => {
    const dirty =
      '<p onclick="alert(1)">Hi</p><script>evil()</script><a href="javascript:alert(1)">x</a>';
    const clean = sanitizeHtml(dirty);
    expect(clean).not.toMatch(/script/i);
    expect(clean).not.toMatch(/onclick/i);
    expect(clean).not.toMatch(/javascript:/i);
    expect(clean).toContain("Hi");
  });

  it("converts HTML to plain text with line breaks", () => {
    expect(htmlToText("<p>Hello</p><p>World</p>")).toBe("Hello\nWorld");
    expect(htmlToText("A<br>B")).toBe("A\nB");
    expect(htmlToText("<p>&amp; &lt;ok&gt;</p>")).toBe("& <ok>");
  });

  it("detects empty editor HTML", () => {
    expect(isEmptyHtml("<p></p>")).toBe(true);
    expect(isEmptyHtml("<p><br></p>")).toBe(true);
    expect(isEmptyHtml("<p>hi</p>")).toBe(false);
  });
});
