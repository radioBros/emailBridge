/** Event catalogue (S3) plus family wildcards for filters. */
export const EVENT_TYPES = [
  "account.*",
  "account.added",
  "account.updated",
  "account.deleted",
  "account.initialized",
  "account.state_changed",
  "account.auth_failed",
  "account.auth_succeeded",
  "account.connect_failed",
  "mailbox.*",
  "mailbox.created",
  "mailbox.deleted",
  "mailbox.reset",
  "message.*",
  "message.received",
  "message.updated",
  "message.deleted",
  "message.missing",
  "message.sent",
  "message.delivery_attempt_failed",
  "message.failed",
  "message.captured",
  "endpoint.test",
] as const;

export type EventFamily = "account" | "mailbox" | "message" | "endpoint" | "other";

export const familyOf = (type: string): EventFamily => {
  const fam = type.split(".")[0] ?? "";
  if (fam === "account" || fam === "mailbox" || fam === "message" || fam === "endpoint") {
    return fam;
  }
  return "other";
};

export const familyColor = (type: string): string => {
  switch (familyOf(type)) {
    case "account":
      return "primary";
    case "mailbox":
      return "secondary";
    case "message":
      return "info";
    case "endpoint":
      return "warning";
    default:
      return "grey";
  }
};

export const toApiDate = (value: string): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};
