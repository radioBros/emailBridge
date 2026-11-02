/** Role chip colour by node role name. */
export const roleColor = (role: string): string => {
  switch (role) {
    case "api":
      return "primary";
    case "sync":
      return "info";
    case "submit":
      return "success";
    case "webhook":
      return "secondary";
    case "scheduler":
      return "accent";
    default:
      return "grey";
  }
};

/** Friendly queue label key suffix from stream name. */
export const queueLabelKey = (name: string): string => {
  const short = name.replace(/^EMAILBRIDGE_/, "").toLowerCase();
  return short || name.toLowerCase();
};
