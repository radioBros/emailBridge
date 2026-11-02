export const TLS_OPTIONS = ["implicit", "starttls", "none"] as const;
export const POLICY_OPTIONS = ["standard", "atMostOnce"] as const;
export const KIND_OPTIONS = ["smtp", "ses", "postmark", "mailgun", "sendgrid", "brevo"] as const;

export const isAPIKind = (kind: string): boolean => kind !== "" && kind !== "smtp";

export const tlsLabelKey = (tls: string): string => {
  if (tls === "implicit" || tls === "starttls" || tls === "none") return `gateways.tls_${tls}`;
  return "gateways.tls_none";
};

export const kindLabelKey = (kind: string): string => {
  const k = kind || "smtp";
  if ((KIND_OPTIONS as readonly string[]).includes(k)) return `gateways.kind_${k}`;
  return "gateways.kind_smtp";
};

export const policyLabelKey = (policy: string): string => {
  if (policy === "atMostOnce") return "gateways.policy_atMostOnce";
  return "gateways.policy_standard";
};

export const stateLabelKey = (state: string): string => {
  const known = ["queued", "running", "succeeded", "failed", "timedOut"];
  return known.includes(state) ? `gateways.state_${state}` : "gateways.state_running";
};

export const folderLabelKey = (folder: string): string => {
  if (folder === "inbox" || folder === "spam" || folder === "other") {
    return `gateways.folder_${folder}`;
  }
  return "gateways.folder_other";
};

export const authResultLabelKey = (result: string): string => {
  const known = ["pass", "fail", "none", "neutral", "softfail", "temperror", "permerror"];
  return known.includes(result) ? `gateways.auth_${result}` : "gateways.auth_none";
};

export const authResultColor = (result: string): string => {
  if (result === "pass") return "success";
  if (result === "fail" || result === "permerror") return "error";
  if (result === "softfail" || result === "temperror") return "warning";
  return "secondary";
};
