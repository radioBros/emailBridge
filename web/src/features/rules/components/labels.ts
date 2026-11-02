import {
  mdiClockOutline,
  mdiCodeJson,
  mdiEmailOutline,
  mdiFolderOutline,
  mdiLightningBoltOutline,
  mdiPaperclip,
  mdiShieldCheckOutline,
  mdiTagOutline,
  mdiInboxArrowDown,
  mdiSend,
  mdiFileDocumentEditOutline,
  mdiAlertOctagonOutline,
  mdiDeleteOutline,
  mdiArchiveOutline,
  mdiEmailMultipleOutline,
  mdiStarOutline,
  mdiFolderOpenOutline,
} from "@mdi/js";
import type { FieldGroup } from "@/features/rules/model/builder";

type T = (key: string, named?: Record<string, unknown>) => string;
type TE = (key: string) => boolean;

export const GROUP_ICONS: Record<FieldGroup, string> = {
  event: mdiLightningBoltOutline,
  mailbox: mdiFolderOutline,
  message: mdiEmailOutline,
  attachments: mdiPaperclip,
  classification: mdiTagOutline,
  trust: mdiShieldCheckOutline,
  time: mdiClockOutline,
  payload: mdiCodeJson,
};

export const SPECIAL_USE_ICONS: Record<string, string> = {
  "\\Inbox": mdiInboxArrowDown,
  "\\Sent": mdiSend,
  "\\Drafts": mdiFileDocumentEditOutline,
  "\\Junk": mdiAlertOctagonOutline,
  "\\Trash": mdiDeleteOutline,
  "\\Archive": mdiArchiveOutline,
  "\\All": mdiEmailMultipleOutline,
  "\\Flagged": mdiStarOutline,
};

export const folderIcon = (specialUse?: string): string =>
  (specialUse && SPECIAL_USE_ICONS[specialUse]) || mdiFolderOpenOutline;

const keyPart = (s: string): string => s.replace(/[.\\$]/g, "_");

/** Label of a builder field: the translated name, or the payload path itself. */
export const fieldLabel = (t: T, te: TE, field: string): string => {
  const key = `rules.field.${keyPart(field)}`;
  return te(key) ? t(key) : field;
};

export const opLabel = (t: T, op: string): string => t(`rules.op.${op}`);

/** Translated value of an enum-ish option, falling back to the raw value. */
export const enumLabel = (t: T, te: TE, field: string, value: string): string => {
  const key = `rules.enum.${keyPart(field)}.${keyPart(value)}`;
  return te(key) ? t(key) : value;
};
