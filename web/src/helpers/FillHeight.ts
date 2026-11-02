/**
 * Fill height — the geometry behind `useFillHeight`: how tall a box must be so it ends exactly
 * at the viewport bottom, measured from where it actually sits (no estimate of the chrome above).
 *
 *   height = max(min, viewport − top − below)
 *
 * `top` is the box's top in DOCUMENT coordinates (client rect top + page scroll), so a scrolled
 * page measures the same as an unscrolled one. `below` is everything that must still fit under
 * the box: for the box and each of its ancestors, the bottom padding + border of the parent and
 * whatever the parent stacks after it (a card's footer bar, a note under the table). None of it
 * depends on the box's own height, so setting the height never feeds back into the next
 * measurement. Ancestors' own heights are never read: layout wrappers carry viewport
 * min-heights, and cards may be stretched (`height: 100%`), whose slack would depend on the box.
 */

export type FillHeightInput = {
  /** Viewport height (px). */
  viewport: number;
  /** Top of the box in document coordinates (px). */
  top: number;
  /** Space that must stay free under the box (px). */
  below: number;
  /** Floor (px): below it the page scrolls instead of squashing the box. */
  min: number;
};

/** The height that makes the box end at the viewport bottom, never below `min`; whole pixels. */
export const fillHeight = ({ viewport, top, below, min }: FillHeightInput): number =>
  Math.max(min, Math.floor(viewport - top - below));

const px = (value: string): number => {
  const n = Number.parseFloat(value);
  return Number.isFinite(n) ? n : 0;
};

/** Space a node's later in-flow siblings take BELOW it (beside it — the next column — is not below). */
const spaceAfter = (node: HTMLElement): number => {
  const bottom = node.getBoundingClientRect().bottom;
  // Own margin first; may be negative (a gutter pulled back by a negative row margin).
  let end = bottom + px(getComputedStyle(node).marginBottom);
  for (let sib = node.nextElementSibling; sib; sib = sib.nextElementSibling) {
    if (!(sib instanceof HTMLElement)) continue;
    const style = getComputedStyle(sib);
    if (style.display === "none" || style.position === "absolute" || style.position === "fixed") continue;
    const rect = sib.getBoundingClientRect();
    if (rect.height === 0 || rect.top < bottom - 0.5) continue;
    end = Math.max(end, rect.bottom + px(style.marginBottom));
  }
  return end - bottom;
};

/**
 * Space under `el` up to the document bottom: walking up from `el`, what each node's parent
 * stacks after it plus the parent's bottom padding and border (the card's border, the page
 * container's padding, the layout's bottom offset for a bottom bar).
 */
export const spaceBelow = (el: HTMLElement): number => {
  let below = 0;
  for (let node: HTMLElement = el; node.parentElement && node !== document.documentElement; node = node.parentElement) {
    const style = getComputedStyle(node.parentElement);
    below += spaceAfter(node) + px(style.paddingBottom) + px(style.borderBottomWidth);
  }
  return below;
};

/** The viewport height a page lays out against (no horizontal scrollbar). */
export const viewportHeight = (): number => document.documentElement.clientHeight || window.innerHeight;

/**
 * Screen box: the CSS height of a list box as tall as the screen below the app's fixed bars — the
 * viewport (`100dvh`) minus the layout's top (app bar) and bottom (bottom navigation) offsets and the
 * device's safe-area insets, never below `min`. The page scrolls until what sits above the list is
 * gone; then the list fills the screen and scrolls inside.
 */
export const screenBoxHeight = ({ top, bottom, min }: { top: number; bottom: number; min: number }): string =>
  `max(${min}px, calc(100dvh - ${Math.max(0, top)}px - ${Math.max(0, bottom)}px - ` +
  "env(safe-area-inset-top, 0px) - env(safe-area-inset-bottom, 0px)))";
