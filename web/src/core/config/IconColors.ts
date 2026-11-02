/**
 * Central icon colour map — navigation sections and feature accents.
 *
 * Knobs (change here → applies app-wide):
 * - `ICON_PALETTE` — theme colour tokens used for icons (must exist on light + dark themes)
 * - `NAV_GROUP_ICON_COLOR` — which palette slot each nav group uses
 * - `FEATURE_ICON_COLOR` — optional overrides for page-header / empty-state icons by feature key
 *
 * All palette tokens are contrast-checked (≥ 3:1 on surface) in `tests/contrast.test.ts`.
 * Icons inside coloured buttons keep the button colour — do not wrap those with these helpers.
 */

/** Theme colour keys reserved for icons (defined in VuetifyThemes). */
export const ICON_PALETTE = [
  "icon-indigo",
  "icon-cyan",
  "icon-violet",
  "icon-teal",
  "icon-amber",
  "icon-rose",
] as const;

export type IconPaletteKey = (typeof ICON_PALETTE)[number];

/** Nav group `titleKey` → palette token. */
export const NAV_GROUP_ICON_COLOR: Record<string, IconPaletteKey> = {
  "nav.overview": "icon-indigo",
  "nav.mail": "icon-cyan",
  "nav.delivery": "icon-violet",
  "nav.sending": "icon-teal",
  "nav.organisation": "icon-amber",
  "nav.system": "icon-rose",
};

/** Feature / screen key → palette token (page headers, empty states, stat tiles). */
export const FEATURE_ICON_COLOR: Record<string, IconPaletteKey> = {
  dashboard: "icon-indigo",
  accounts: "icon-cyan",
  outbox: "icon-cyan",
  sandbox: "icon-cyan",
  messages: "icon-cyan",
  webhooks: "icon-violet",
  routes: "icon-violet",
  deliveries: "icon-violet",
  events: "icon-violet",
  templates: "icon-teal",
  gateways: "icon-teal",
  suppression: "icon-teal",
  members: "icon-amber",
  "api-keys": "icon-amber",
  audit: "icon-amber",
  "oauth-apps": "icon-amber",
  health: "icon-rose",
  logs: "icon-rose",
  settings: "icon-rose",
  branding: "icon-rose",
  cluster: "icon-rose",
  integrations: "icon-rose",
};

/** App chrome — top-bar buttons and the profile menu → palette token. */
export const CHROME_ICON_COLOR = {
  menu: "icon-indigo",
  notifications: "icon-amber",
  theme: "icon-violet",
  language: "icon-teal",
  profile: "icon-cyan",
  signOut: "icon-rose",
} as const satisfies Record<string, IconPaletteKey>;

export const iconColorForNavGroup =(titleKey: string): IconPaletteKey =>
  NAV_GROUP_ICON_COLOR[titleKey] ?? "icon-indigo";

export const iconColorForFeature = (feature: string): IconPaletteKey =>
  FEATURE_ICON_COLOR[feature] ?? "icon-indigo";
