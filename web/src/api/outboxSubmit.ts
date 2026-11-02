import { api } from "@/api/client";

// local until schema regen
export interface ComposeSubmitBody {
  to: Array<{ address: string; name?: string }>;
  subject?: string;
  html?: string;
  text?: string;
  trackOpens?: boolean;
  trackClicks?: boolean;
  listId?: string;
}

export interface ComposeAccepted {
  id: string;
  messageId: string;
  state: string;
}

export const submitOutbound = (
  accountId: string,
  body: ComposeSubmitBody,
): Promise<ComposeAccepted> =>
  api.post<ComposeAccepted>(
    `/accounts/${encodeURIComponent(accountId)}/outbound-messages`,
    body,
  );
