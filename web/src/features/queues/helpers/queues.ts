import { mdiCalendarClock, mdiEmailFastOutline, mdiShieldAccountOutline, mdiWebhook } from "@mdi/js";
import { DeliveryStates, OutboundStates, type StatusSpec } from "@/constants/Statuses";
import type { QueueFilter, QueueItemCounts, QueueName, QueueSummary } from "@/api/queues";

/** One cancellable queue as the page shows it (W72 §9). */
export interface QueueDef {
  name: Exclude<QueueName, "EMAILBRIDGE_EVENTS">;
  /** i18n suffix: queues.queue_<key>. */
  key: "sending" | "webhooks" | "systemMail";
  icon: string;
  /** Queue states in display order (the API's `state` filter values). */
  states: string[];
  hasAccount: boolean;
  hasEndpoint: boolean;
  /** System mail is instance-level: the instance owner only. */
  ownerOnly: boolean;
  /** Permission (GET /v1/me permissions) that cancels in the organisation scope. */
  writePermission: string;
}

export const QUEUE_DEFS: QueueDef[] = [
  {
    name: "EMAILBRIDGE_SUBMIT",
    key: "sending",
    icon: mdiEmailFastOutline,
    states: ["scheduled", "queued", "retrying", "sending"],
    hasAccount: true,
    hasEndpoint: false,
    ownerOnly: false,
    writePermission: "outbound.write",
  },
  {
    name: "EMAILBRIDGE_DELIVERY",
    key: "webhooks",
    icon: mdiWebhook,
    states: ["pending", "retrying", "deadLettered", "delivering"],
    hasAccount: true,
    hasEndpoint: true,
    ownerOnly: false,
    writePermission: "webhooks.write",
  },
  {
    name: "EMAILBRIDGE_SYSMAIL",
    key: "systemMail",
    icon: mdiShieldAccountOutline,
    states: ["pending", "retrying", "sending"],
    hasAccount: false,
    hasEndpoint: false,
    ownerOnly: true,
    writePermission: "cluster.operate",
  },
];

export const EVENTS_QUEUE = "EMAILBRIDGE_EVENTS";

export const queueDefOf = (name: string): QueueDef | undefined => QUEUE_DEFS.find((d) => d.name === name);

/** The queues a caller can open: system mail for the instance owner only. */
export const visibleQueues = (isOwner: boolean): QueueDef[] => QUEUE_DEFS.filter((d) => isOwner || !d.ownerOnly);

/** Whether the caller may cancel in this queue (instance owner may cancel everywhere). */
export const canCancelIn = (def: QueueDef, permissions: string[], isOwner: boolean): boolean =>
  isOwner || (!def.ownerOnly && permissions.includes(def.writePermission));

const IN_FLIGHT = new Set(["sending", "delivering"]);

export const isInFlight = (state: string): boolean => IN_FLIGHT.has(state);

/** Chip colour + icon of a queue state (in flight reads as "busy", not as an error). */
export const queueStateSpec = (state: string): StatusSpec => {
  switch (state) {
    case "scheduled":
      return { ...OutboundStates.scheduled, icon: mdiCalendarClock };
    case "queued":
      return OutboundStates.queued;
    case "pending":
      return DeliveryStates.pending;
    case "retrying":
      return DeliveryStates.retrying;
    case "deadLettered":
      return DeliveryStates.deadLettered;
    case "delivering":
      return DeliveryStates.delivering;
    case "sending":
      return OutboundStates.sending;
    default:
      return DeliveryStates.pending;
  }
};

/** Waiting work of a queue (ready + scheduled + retrying + dead letters); in-flight is shown apart. */
export const backlogOf = (c: QueueItemCounts | undefined): number =>
  c ? c.waiting + c.scheduled + c.retrying + c.deadLettered : 0;

export interface Age {
  unit: "minutes" | "hours" | "days";
  value: number;
}

/** Age of the oldest record, rounded to one unit (under a minute counts as 1 minute). */
export const ageOf = (iso: string | undefined, now: number = Date.now()): Age | null => {
  if (!iso) return null;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return null;
  const minutes = Math.max(1, Math.floor((now - t) / 60_000));
  if (minutes < 60) return { unit: "minutes", value: minutes };
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return { unit: "hours", value: hours };
  return { unit: "days", value: Math.floor(hours / 24) };
};

/** Date-range field value (yyyy-mm-dd or ISO) → RFC 3339 for the API; empty → undefined. */
export const toApiDate = (value: string, endOfDay = false): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return undefined;
  if (endOfDay && /^\d{4}-\d{2}-\d{2}$/.test(value)) d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString();
};

/** The filter body for "cancel all matching" from the page's filter controls. */
export const filterBody = (f: {
  state: string;
  accountId: string;
  endpointId: string;
  from: string;
  to: string;
  q: string;
}): QueueFilter => {
  const out: QueueFilter = {};
  if (f.state) out.state = f.state;
  if (f.accountId) out.accountId = f.accountId;
  if (f.endpointId) out.endpointId = f.endpointId;
  const from = toApiDate(f.from);
  const to = toApiDate(f.to, true);
  if (from) out.createdFrom = from;
  if (to) out.createdTo = to;
  if (f.q.trim()) out.q = f.q.trim();
  return out;
};

export const isEmptyFilter = (f: QueueFilter): boolean => Object.keys(f).length === 0;

/** Summary of one queue by name. */
export const summaryOf = (rows: QueueSummary[], name: string): QueueSummary | undefined =>
  rows.find((r) => r.name === name);

/** API sort parameter from a table sort item. */
export const sortParam = (sortBy: { key: string; order: "asc" | "desc" }[]): string => {
  const first = sortBy[0];
  if (!first || (first.key !== "createdAt" && first.key !== "nextAttemptAt")) return "createdAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};
