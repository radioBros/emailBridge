import VueTippy, { setDefaultProps } from "vue-tippy";
import type { Instance } from "tippy.js";
import "tippy.js/dist/tippy.css";
import "tippy.js/animations/shift-away-subtle.css";
import "./tippy.scss";

/**
 * The app's ONE tooltip: every hint is `v-tippy="text"` — never `<v-tooltip>` and never a native
 * `title=` on a control (tests/tooltip-consistency.test.ts keeps it that way). The look lives in
 * `tippy.scss` (theme "eb", follows the Vuetify light/dark theme); the behaviour is set once here.
 *
 * A tooltip is a hint, not an accessible name: icon-only controls keep their `aria-label`.
 */

/** Plain-text content of a tooltip, or "" when it has none. */
const contentText = (instance: Instance): string => {
  const raw = instance.props.content;
  if (typeof raw === "string") return raw.trim();
  if (typeof raw === "number") return String(raw);
  return "";
};

/** Keep role=tooltip nodes named for axe (aria-tooltip-name). */
const syncTooltipName = (instance: Instance): void => {
  const box = instance.popper?.querySelector(".tippy-box");
  if (!(box instanceof HTMLElement)) return;
  const name = contentText(instance);
  if (name) box.setAttribute("aria-label", name);
  else box.removeAttribute("aria-label");
};

/** Optional hints (`v-tippy="maybeUndefined"`) render nothing instead of an empty box. */
const skipEmpty = (instance: Instance): false | void => {
  const raw = instance.props.content;
  const isText = typeof raw === "string" || typeof raw === "number" || raw == null;
  if (isText && !contentText(instance)) return false;
};

/** Typed by vue-tippy's own `setDefaultProps` (its `delay` type is stricter than tippy.js's). */
export const TOOLTIP_DEFAULTS: Parameters<typeof setDefaultProps>[0] = {
  theme: "eb",
  placement: "top",
  arrow: true,
  animation: "shift-away-subtle",
  // Hover and keyboard focus (tippy default); explicit so a future tippy upgrade cannot drop focus.
  trigger: "mouseenter focus",
  // Wait before showing so a pointer passing over a toolbar does not flash hints; hide at once.
  delay: [400, 0],
  duration: [150, 100],
  maxWidth: 280,
  offset: [0, 8],
  interactive: false,
  // Phones: a tap is an action, not a hover. Show only on a deliberate long press and hide when the
  // finger lifts — a tooltip never sticks on a touch screen (and focus after a tap does not open it).
  touch: ["hold", 500],
};

setDefaultProps({
  ...TOOLTIP_DEFAULTS,
  onShow: skipEmpty,
  onMount: syncTooltipName,
  onAfterUpdate: syncTooltipName,
});

export default VueTippy;
