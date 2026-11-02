export const SUPPRESSION_KINDS = ["manual", "unsubscribe", "bounce", "complaint"] as const;
export const SUPPRESSION_SOURCES = [
  "manual",
  "unsubscribe",
  "bounce",
  "complaint",
  "import",
] as const;

export const kindLabelKey = (kind: string): string => {
  if ((SUPPRESSION_KINDS as readonly string[]).includes(kind)) {
    return `suppression.kind_${kind}`;
  }
  return "suppression.kind_manual";
};

export const sourceLabelKey = (source: string): string => {
  if ((SUPPRESSION_SOURCES as readonly string[]).includes(source)) {
    return `suppression.source_${source}`;
  }
  return "suppression.source_manual";
};

export const kindColor = (kind: string): string => {
  switch (kind) {
    case "unsubscribe":
      return "info";
    case "bounce":
      return "warning";
    case "complaint":
      return "error";
    default:
      return "secondary";
  }
};
