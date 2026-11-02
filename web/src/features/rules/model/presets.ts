import {
  mdiAccountArrowLeftOutline,
  mdiCertificateOutline,
  mdiEmailFastOutline,
  mdiEmailArrowLeftOutline,
  mdiFeather,
  mdiInboxArrowDown,
  mdiPaperclip,
  mdiEmailAlertOutline,
} from "@mdi/js";
import type { RuleGroup } from "@/api/rules";
import { ACCOUNT_REF, startingMap, type MapNode } from "@/features/rules/model/mapTree";

/**
 * Ready-made rules. The two "shared key" presets reproduce a common EmailEngine webhook setup (a shared key
 * plus a slim body): the key is a SECRET in `settings.env`, so a preset asks for the secret's NAME and never
 * holds a value.
 */

export interface PresetResult {
  /** New builder (undefined = keep the filter). */
  builder?: RuleGroup;
  /** New map tree (undefined = keep the map; null = no map). */
  map?: MapNode | null;
  /** Event types the endpoint must subscribe to. */
  events?: string[];
}

export interface Preset {
  id: string;
  icon: string;
  color: string;
  /** Asks for the env secret that holds the shared key. */
  needsSecret?: boolean;
  apply: (secretName: string) => PresetResult;
}

export const DEFAULT_SECRET_NAME = "webhookKey";

export const OUTBOUND_STATUS_EVENTS = ["message.sent", "message.failed", "message.delivery_attempt_failed"];

const inboxBuilder = (): RuleGroup => ({
  match: "all",
  conditions: [
    { field: "type", op: "in", value: ["message.received"] },
    { field: "isInbox", op: "eq", value: true },
  ],
});

/** Inbound mail with shared key: new mail in the Inbox → `{ key, body: { id, date, account, event, headers, emailid, subject } }`. */
export const keyedInboundMap = (secretName: string): MapNode => ({
  kind: "object",
  entries: [
    { key: "key", node: { kind: "secret", name: secretName } },
    {
      key: "body",
      node: {
        kind: "object",
        entries: [
          { key: "id", node: { kind: "field", path: "data.messageId" } },
          { key: "date", node: { kind: "field", path: "event.time" } },
          { key: "account", node: { kind: "expr", source: ACCOUNT_REF } },
          { key: "event", node: { kind: "constant", value: "messageNew" } },
          { key: "headers", node: { kind: "field", path: "data.headers" } },
          { key: "emailid", node: { kind: "field", path: "data.messageIdHeader" } },
          { key: "subject", node: { kind: "field", path: "data.subject" } },
        ],
      },
    },
  ],
});

export const PRESETS: Preset[] = [
  {
    id: "keyedInbound",
    icon: mdiEmailArrowLeftOutline,
    color: "primary",
    needsSecret: true,
    apply: (secret) => ({
      builder: inboxBuilder(),
      map: keyedInboundMap(secret),
      events: ["message.received"],
    }),
  },
  {
    id: "keyedStatus",
    icon: mdiEmailFastOutline,
    color: "secondary",
    needsSecret: true,
    apply: (secret) => ({
      builder: {
        match: "all",
        conditions: [{ field: "type", op: "in", value: [...OUTBOUND_STATUS_EVENTS] }],
      },
      map: {
        kind: "object",
        entries: [
          { key: "key", node: { kind: "secret", name: secret } },
          { key: "body", node: { kind: "field", path: "event" } },
        ],
      },
      events: [...OUTBOUND_STATUS_EVENTS],
    }),
  },
  {
    id: "inboxMail",
    icon: mdiInboxArrowDown,
    color: "info",
    apply: () => ({ builder: inboxBuilder(), events: ["message.received"] }),
  },
  {
    id: "pecReceipts",
    icon: mdiCertificateOutline,
    color: "success",
    apply: () => ({
      builder: { match: "all", conditions: [{ field: "type", op: "in", value: ["pec.receipt"] }] },
      events: ["pec.receipt"],
    }),
  },
  {
    id: "withAttachments",
    icon: mdiPaperclip,
    color: "accent",
    apply: () => ({
      builder: {
        match: "all",
        conditions: [
          { field: "type", op: "in", value: ["message.received"] },
          { field: "hasAttachments", op: "eq", value: true },
        ],
      },
      events: ["message.received"],
    }),
  },
  {
    id: "bounces",
    icon: mdiEmailAlertOutline,
    color: "warning",
    apply: () => ({
      builder: {
        match: "any",
        conditions: [
          { field: "type", op: "in", value: ["message.bounced"] },
          { field: "isBounce", op: "eq", value: true },
        ],
      },
      events: ["message.bounced", "message.received"],
    }),
  },
  {
    id: "notAutoReply",
    icon: mdiAccountArrowLeftOutline,
    color: "info",
    apply: () => ({
      builder: {
        match: "all",
        conditions: [
          { field: "type", op: "in", value: ["message.received"] },
          { field: "isAutoReply", op: "eq", value: false },
        ],
      },
      events: ["message.received"],
    }),
  },
  {
    id: "slimPayload",
    icon: mdiFeather,
    color: "secondary",
    apply: () => ({ map: startingMap("slim") }),
  },
];

export const presetById = (id: string): Preset | undefined => PRESETS.find((p) => p.id === id);
