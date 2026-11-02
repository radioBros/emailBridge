import { api } from "@/api/client";

// local until schema regen (P5.3)

export type DeliveryTestState =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "timedOut";

export type DeliveryTestFolder = "inbox" | "spam" | "other";

export type AuthResultItem = {
  result: string;
  domain?: string;
  selector?: string;
};

export type AuthResults = {
  spf: AuthResultItem;
  dkim?: AuthResultItem[];
  dmarc: AuthResultItem;
  arc?: AuthResultItem;
  source?: string;
};

export type DeliveryTestAccepted = {
  id: string;
  jobId: string;
  state: DeliveryTestState | string;
};

export type DeliveryTestView = {
  id: string;
  organizationId: string;
  accountId: string;
  to: string;
  state: DeliveryTestState | string;
  outboundId?: string;
  messageId?: string;
  folder?: DeliveryTestFolder | string;
  mailboxPath?: string;
  latencyMs?: number;
  authResults?: AuthResults | null;
  error?: { code?: string; detail?: string; title?: string; status?: number } | null;
  jobId?: string;
  createdAt: string;
  finishedAt?: string | null;
};

export type DeliveryTestCreate = {
  accountId: string;
  to: string;
  timeout?: string;
};

export const createDeliveryTest = (body: DeliveryTestCreate): Promise<DeliveryTestAccepted> =>
  api.post<DeliveryTestAccepted>("/delivery-tests", body);

export const getDeliveryTest = (id: string): Promise<DeliveryTestView> =>
  api.get<DeliveryTestView>(`/delivery-tests/${encodeURIComponent(id)}`);

export const TERMINAL_DELIVERY_TEST_STATES = new Set([
  "succeeded",
  "failed",
  "timedOut",
]);

export const isTerminalDeliveryTest = (state: string): boolean =>
  TERMINAL_DELIVERY_TEST_STATES.has(state);
