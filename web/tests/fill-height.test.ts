import { afterEach, describe, expect, it } from "vitest";
import { fillHeight, screenBoxHeight, spaceBelow } from "@/helpers/FillHeight";

describe("FillHeight — fillHeight", () => {
  it("takes the space left between the box top and the viewport bottom", () => {
    expect(fillHeight({ viewport: 900, top: 180, below: 24, min: 360 })).toBe(696);
  });

  it("never goes below the floor (the page scrolls instead)", () => {
    expect(fillHeight({ viewport: 600, top: 400, below: 24, min: 360 })).toBe(360);
  });

  it("rounds down to whole pixels so the page never overflows by a fraction", () => {
    expect(fillHeight({ viewport: 900, top: 180.4, below: 24.3, min: 0 })).toBe(695);
  });

  it("does not depend on where the page is scrolled (top is in document coordinates)", () => {
    const scrollY = 120;
    const clientTop = 60;
    expect(fillHeight({ viewport: 900, top: clientTop + scrollY, below: 24, min: 360 })).toBe(696);
  });
});

describe("FillHeight — spaceBelow", () => {
  afterEach(() => {
    document.body.innerHTML = "";
    document.body.removeAttribute("style");
  });

  it("sums the bottom padding and border of every ancestor", () => {
    document.body.innerHTML = `
      <main style="padding-bottom: 24px">
        <div style="border-bottom: 1px solid red; padding-bottom: 0">
          <section id="box"></section>
        </div>
      </main>`;
    const box = document.getElementById("box") as HTMLElement;
    expect(spaceBelow(box)).toBe(25);
  });

  it("is zero for a box with nothing under it", () => {
    document.body.style.padding = "0";
    document.body.innerHTML = `<section id="box"></section>`;
    expect(spaceBelow(document.getElementById("box") as HTMLElement)).toBe(0);
  });
});

describe("FillHeight — screenBoxHeight", () => {
  it("is the viewport minus the measured bars and the safe-area insets, with a floor", () => {
    expect(screenBoxHeight({ top: 64, bottom: 0, min: 360 })).toBe(
      "max(360px, calc(100dvh - 64px - 0px - env(safe-area-inset-top, 0px) - env(safe-area-inset-bottom, 0px)))",
    );
  });

  it("never subtracts a negative bar", () => {
    expect(screenBoxHeight({ top: -5, bottom: 56, min: 300 })).toContain("100dvh - 0px - 56px");
  });
});
