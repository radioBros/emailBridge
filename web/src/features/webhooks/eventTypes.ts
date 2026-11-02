/** CloudEvents types from S3 / `api/events/*.json` (type without schema version). */
export const WEBHOOK_EVENT_TYPES = [
  "account.added",
  "account.updated",
  "account.deleted",
  "account.initialized",
  "account.state_changed",
  "account.auth_failed",
  "account.auth_succeeded",
  "account.connect_failed",
  "mailbox.created",
  "mailbox.deleted",
  "mailbox.reset",
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

export type WebhookEventType = (typeof WEBHOOK_EVENT_TYPES)[number];

export const WEBHOOK_KINDS = ["http", "nats", "kafka", "sqs", "sns", "pubsub"] as const;

export type WebhookKind = (typeof WEBHOOK_KINDS)[number];

export const defaultRetry = () => ({
  maxAttempts: 10,
  backoffBase: "5s",
  backoffJitter: 0.2,
  timeout: "30s",
});

export const defaultDelivery = () => ({
  ordering: "strict" as const,
  maxConcurrency: 8,
  contentType: "json" as const,
  retryOn4xx: false,
  autoDisableAfter: "120h",
});

/**
 * Client-generated Standard Webhooks secret: `whsec_` + std base64 of 32 random bytes
 * (`pkg/webhookverify`). Never Math.random.
 */
export const generateWebhookSecret = (): string => {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return `whsec_${btoa(bin)}`;
};

export const WEBHOOK_SECRET_RE = /^whsec_[A-Za-z0-9+/]+=*$/;
