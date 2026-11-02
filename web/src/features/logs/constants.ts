export const LOG_CATEGORIES = [
  "system",
  "api",
  "auth",
  "audit",
  "event",
  "sync",
  "protocol",
  "send",
  "delivery",
  "rules",
  "job",
  "cluster",
  "integration",
  "security",
  "crash",
] as const;

export type LogCategory = (typeof LOG_CATEGORIES)[number];
