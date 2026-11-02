import { OutboundStates, type OutboundState, type StatusSpec } from "@/constants/Statuses";

/** States that accept DELETE cancel (S2 / S6). */
export const CANCELLABLE_STATES = new Set<OutboundState>(["queued", "scheduled"]);

export const OUTBOUND_STATE_KEYS = Object.keys(OutboundStates) as OutboundState[];

export const isOutboundState = (value: string): value is OutboundState =>
  value in OutboundStates;

export const outboundStatusOf = (state: string): StatusSpec =>
  isOutboundState(state) ? OutboundStates[state] : OutboundStates.failed;

export const canCancelOutbound = (state: string): boolean =>
  isOutboundState(state) && CANCELLABLE_STATES.has(state);

/** Plain-language note shown wherever `outcomeUnknown` appears. */
export const OUTCOME_UNKNOWN_PLAIN =
  'EmailBridge got no final answer from the mail server after sending the message, so it may or may not have been accepted. In "at most once" mode it is not retried, to avoid sending it twice.';

export const formatRecipients = (to: string[] | null | undefined): string => {
  if (!to?.length) return "—";
  if (to.length === 1) return to[0]!;
  return `${to[0]} +${to.length - 1}`;
};

export const formatAttempts = (count: number | undefined, max: number | undefined): string => {
  const n = count ?? 0;
  if (max == null) return String(n);
  return `${n}/${max}`;
};

/** datetime-local value → RFC 3339 UTC, or pass through if already ISO. */
export const toApiDate = (value: string): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};
