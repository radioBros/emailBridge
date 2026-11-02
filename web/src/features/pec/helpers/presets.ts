import type { PECDefaults } from "@/api/pec";

/** Mutable form fields the PEC preset banner can suggest. */
export interface PecFormTarget {
  smtpPolicy: string;
  syncPaths: string;
  idlePaths: string;
  pecEnabled: boolean;
}

export const applyPecEnabled = (form: PecFormTarget): void => {
  form.pecEnabled = true;
};

export const applyPecInboxOnly = (
  form: PecFormTarget,
  defaults?: PECDefaults | null,
): void => {
  const paths = defaults?.syncPaths?.filter(Boolean);
  const joined = paths?.length ? paths.join(", ") : "INBOX";
  form.syncPaths = joined;
  form.idlePaths = "INBOX";
};

export const applyPecSendPolicy = (
  form: PecFormTarget,
  defaults?: PECDefaults | null,
): void => {
  const policy = defaults?.sendPolicy || "atMostOnce";
  form.smtpPolicy = policy;
};

export const joinPaths = (paths: string[] | null | undefined, fallback = "INBOX"): string => {
  const cleaned = (paths ?? []).map((p) => p.trim()).filter(Boolean);
  return cleaned.length ? cleaned.join(", ") : fallback;
};
