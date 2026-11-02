/** IMAP system flags used by the message browser. */

export const FLAG_SEEN = "\\Seen";
export const FLAG_FLAGGED = "\\Flagged";
export const FLAG_ANSWERED = "\\Answered";
export const FLAG_DRAFT = "\\Draft";
export const FLAG_DELETED = "\\Deleted";

export const hasFlag = (flags: string[] | undefined, flag: string): boolean =>
  (flags ?? []).some((f) => f.toLowerCase() === flag.toLowerCase());

export const isUnread = (flags: string[] | undefined): boolean => !hasFlag(flags, FLAG_SEEN);

export const isFlagged = (flags: string[] | undefined): boolean => hasFlag(flags, FLAG_FLAGGED);

/** Soft signal some servers set; not in the index envelope yet. */
export const hasAttachmentFlag = (flags: string[] | undefined): boolean =>
  (flags ?? []).some((f) => /attachment/i.test(f));

export type FlagFilter = "" | "unread" | "flagged";

export const listFlagParam = (filter: FlagFilter): string | undefined => {
  if (filter === "flagged") return FLAG_FLAGGED;
  return undefined;
};
