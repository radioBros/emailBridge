/** Translate copy-to-Sent / attempt stage / kind for display. */

export const COPY_TO_SENT_STATES = ["pending", "done", "skipped", "failed"] as const;

export const ATTEMPT_STAGES = [
  "connect",
  "ehlo",
  "auth",
  "mailFrom",
  "rcptTo",
  "data",
  "dataEnd",
] as const;

export const ATTEMPT_KINDS = [
  "temporary",
  "permanent",
  "outcome_unknown",
  "policy",
  "cancelled",
] as const;

export type CopyToSentState = (typeof COPY_TO_SENT_STATES)[number];
export type AttemptStage = (typeof ATTEMPT_STAGES)[number];
export type AttemptKind = (typeof ATTEMPT_KINDS)[number];

export const copyToSentLabelKey = (state: string): string =>
  COPY_TO_SENT_STATES.includes(state as CopyToSentState)
    ? `outbox.copy_state_${state}`
    : "";

export const attemptStageLabelKey = (stage: string): string =>
  ATTEMPT_STAGES.includes(stage as AttemptStage) ? `outbox.stage_${stage}` : "";

export const attemptKindLabelKey = (kind: string): string =>
  ATTEMPT_KINDS.includes(kind as AttemptKind) ? `outbox.kind_${kind}` : "";
