import {
  mdiAccountGroup,
  mdiAccountKey,
  mdiCog,
  mdiPaletteOutline,
  mdiEmailOutline,
  mdiFileDocumentOutline,
  mdiHeartPulse,
  mdiInbox,
  mdiKeyVariant,
  mdiLan,
  mdiMailboxOutline,
  mdiOfficeBuildingOutline,
  mdiRouter,
  mdiSend,
  mdiShieldKey,
  mdiTextBoxOutline,
  mdiTextSearch,
  mdiTrayFull,
  mdiViewDashboard,
  mdiWebhook,
  mdiBellOutline,
  mdiFormatListChecks,
} from "@mdi/js";
import { iconColorForNavGroup, type IconPaletteKey } from "@/core/config/IconColors";

export interface NavItem {
  titleKey: string;
  icon: string;
  to: string;
  /** Theme colour token from IconColors (nav group palette). */
  color: IconPaletteKey;
  /** When true, the screen is not built yet — route shows coming-soon. */
  comingSoon?: boolean;
  /** When true, only instance owners see the item in the drawer. */
  instanceOwnerOnly?: boolean;
}

export interface NavGroup {
  titleKey: string;
  items: NavItem[];
}

const withColor = (
  groupKey: string,
  items: Omit<NavItem, "color">[],
): NavItem[] => {
  const color = iconColorForNavGroup(groupKey);
  return items.map((item) => ({ ...item, color }));
};

/** Navigation groups from S13 §3. Icon colours from IconColors.ts. */
export const navigation: NavGroup[] = [
  {
    titleKey: "nav.overview",
    items: withColor("nav.overview", [
      { titleKey: "nav.dashboard", icon: mdiViewDashboard, to: "/" },
    ]),
  },
  {
    titleKey: "nav.mail",
    items: withColor("nav.mail", [
      { titleKey: "nav.accounts", icon: mdiMailboxOutline, to: "/accounts" },
      { titleKey: "nav.outbox", icon: mdiSend, to: "/outbox" },
      { titleKey: "nav.sandbox", icon: mdiInbox, to: "/sandbox" },
      {
        titleKey: "nav.messages",
        icon: mdiEmailOutline,
        to: "/messages",
      },
      { titleKey: "nav.search", icon: mdiTextSearch, to: "/search" },
    ]),
  },
  {
    titleKey: "nav.delivery",
    items: withColor("nav.delivery", [
      { titleKey: "nav.webhooks", icon: mdiWebhook, to: "/webhooks" },
      { titleKey: "nav.routes", icon: mdiRouter, to: "/routes" },
      { titleKey: "nav.rule_lists", icon: mdiFormatListChecks, to: "/rule-lists" },
      {
        titleKey: "nav.deliveries",
        icon: mdiLan,
        to: "/deliveries",
      },
      { titleKey: "nav.queues", icon: mdiTrayFull, to: "/queues" },
      {
        titleKey: "nav.events",
        icon: mdiTextBoxOutline,
        to: "/events",
      },
    ]),
  },
  {
    titleKey: "nav.sending",
    items: withColor("nav.sending", [
      {
        titleKey: "nav.templates",
        icon: mdiFileDocumentOutline,
        to: "/templates",
      },
      {
        titleKey: "nav.gateways",
        icon: mdiLan,
        to: "/gateways",
      },
      {
        titleKey: "nav.suppression",
        icon: mdiShieldKey,
        to: "/suppression",
      },
    ]),
  },
  {
    titleKey: "nav.organisation",
    items: withColor("nav.organisation", [
      { titleKey: "nav.members", icon: mdiAccountGroup, to: "/members" },
      { titleKey: "nav.api_keys", icon: mdiKeyVariant, to: "/api-keys" },
      { titleKey: "nav.audit", icon: mdiAccountKey, to: "/audit" },
      { titleKey: "nav.oauth_apps", icon: mdiShieldKey, to: "/oauth-apps" },
    ]),
  },
  {
    titleKey: "nav.system",
    items: withColor("nav.system", [
      { titleKey: "nav.health", icon: mdiHeartPulse, to: "/health/accounts" },
      { titleKey: "nav.notifications", icon: mdiBellOutline, to: "/notifications" },
      { titleKey: "nav.logs", icon: mdiTextBoxOutline, to: "/logs" },
      { titleKey: "nav.settings", icon: mdiCog, to: "/settings" },
      { titleKey: "nav.branding", icon: mdiPaletteOutline, to: "/branding" },
      {
        titleKey: "nav.organizations",
        icon: mdiOfficeBuildingOutline,
        to: "/organizations",
        instanceOwnerOnly: true,
      },
      {
        titleKey: "nav.cluster",
        icon: mdiLan,
        to: "/cluster",
      },
      {
        titleKey: "nav.integrations",
        icon: mdiWebhook,
        to: "/integrations",
      },
    ]),
  },
];
