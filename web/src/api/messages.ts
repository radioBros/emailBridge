import { api, ApiProblemError } from "@/api/client";
import type { components } from "@/api/schema";

// local until schema regen — shapes match internal/messages + P4.1/P4.3

export type AccountView = components["schemas"]["AccountView"];
export type AccountListBody = components["schemas"]["AccountListBody"];

export interface AddressView {
  name?: string;
  address: string;
}

export interface MailboxView {
  id: string;
  accountId: string;
  path: string;
  delimiter?: string;
  specialUse?: string;
  uidValidity: number;
  uidNext?: number;
  highestModseq?: number;
  exists?: number;
  syncMode?: string;
  lastSyncAt?: string;
  status: string;
}

/** Attachment row from MessageView.attachments (W50). local until schema regen. */
export interface AttachmentView {
  id: string;
  filename?: string;
  contentType: string;
  size: number;
  contentId?: string;
  inline?: boolean;
  scan?: "clean" | "infected" | "error" | "skipped" | string;
  url?: string;
}

export interface MessageView {
  id: string;
  accountId: string;
  mailboxId: string;
  uidValidity: number;
  uid: number;
  modseq?: number;
  flags: string[];
  messageIdHeader?: string;
  emailId: string;
  /** Always set for newly indexed rows (silent baseline included). */
  threadId?: string;
  internalDate: string;
  size: number;
  /** Envelope summary from the index (P4.4). */
  subject?: string;
  /** Date header from the index; fall back to internalDate in the UI. */
  date?: string;
  from?: AddressView[];
  to?: AddressView[];
  cc?: AddressView[];
  text?: string;
  html?: string;
  /** Sanitised HTML for display (P4.2); prefer over raw `html`. local until schema regen. */
  webSafeHtml?: string;
  headers?: Record<string, string>;
  textTruncated?: boolean;
  /** Indexed attachments (att_… ids). local until schema regen. */
  attachments?: AttachmentView[];
  categories?: string[];
  labels?: string[];
}

export interface DraftView {
  id: string;
  accountId: string;
  path: string;
  uid?: number;
  uidValidity?: number;
  messageIdHeader?: string;
  subject?: string;
  size: number;
  createdAt: string;
  updatedAt: string;
  text?: string;
  html?: string;
}

export interface MessageListBody {
  data: MessageView[];
  nextCursor?: string | null;
  total?: number | null;
}

export interface MailboxListBody {
  data: MailboxView[];
  nextCursor?: string | null;
}

export interface DraftListBody {
  data: DraftView[];
  nextCursor?: string | null;
  total?: number | null;
}

export interface BatchInput {
  ids: string[];
  action: "flags" | "move" | "delete" | "copy";
  addFlags?: string[];
  removeFlags?: string[];
  setFlags?: string[];
  mailboxId?: string;
  path?: string;
}

export interface BatchResult {
  processed: number;
  failed?: string[];
}

export interface SearchInput {
  mailboxId?: string;
  path?: string;
  text?: string;
  subject?: string;
  from?: string;
  to?: string;
  seen?: boolean;
  unseen?: boolean;
  flagged?: boolean;
  since?: string;
  before?: string;
  header?: { name: string; value: string };
  larger?: number;
  smaller?: number;
  uidMin?: number;
  uidMax?: number;
  labels?: string[];
  gmailRaw?: string;
  useOutlookSearch?: boolean;
}

export interface MessageListQuery {
  mailboxId?: string;
  /** Conversation id — server filters across every mailbox on the account. */
  threadId?: string;
  flag?: string;
  q?: string;
  page?: number;
  limit?: number;
  sort?: string;
  includeTotal?: boolean;
  cursor?: string;
}

export interface GetMessageQuery {
  text?: "plain" | "html" | "both";
  headers?: boolean;
  envelope?: boolean;
  maxBytes?: number;
  markAsSeen?: boolean;
  embedAttachedImages?: boolean;
  preProcessHtml?: boolean;
}

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

const acc = (accountId: string): string => `/accounts/${encodeURIComponent(accountId)}`;

export const searchAccounts = async (q = ""): Promise<AccountView[]> => {
  const res = await api.get<AccountListBody>(
    `/accounts${qs({ page: 1, limit: 50, includeTotal: false, q: q || undefined, sort: "name" })}`,
  );
  return res.data ?? [];
};

export const listMailboxes = (accountId: string, path?: string): Promise<MailboxListBody> =>
  api.get<MailboxListBody>(`${acc(accountId)}/mailboxes${qs({ path, limit: 100 })}`);

export const listMessages = (
  accountId: string,
  query: MessageListQuery = {},
): Promise<MessageListBody> =>
  api.get<MessageListBody>(
    `${acc(accountId)}/messages${qs({
      mailboxId: query.mailboxId,
      threadId: query.threadId,
      flag: query.flag,
      q: query.q,
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      sort: query.sort ?? "-internalDate",
      includeTotal: query.includeTotal ?? true,
      cursor: query.cursor,
    })}`,
  );

export const searchMessages = (
  accountId: string,
  body: SearchInput,
): Promise<MessageListBody> => api.post<MessageListBody>(`${acc(accountId)}/messages/search`, body);

export const getMessage = (
  accountId: string,
  messageId: string,
  query: GetMessageQuery = {},
): Promise<MessageView> =>
  api.get<MessageView>(
    `${acc(accountId)}/messages/${encodeURIComponent(messageId)}${qs({
      text: query.text,
      headers: query.headers ? 1 : undefined,
      envelope: query.envelope ? 1 : undefined,
      maxBytes: query.maxBytes,
      markAsSeen: query.markAsSeen,
      embedAttachedImages: query.embedAttachedImages ? 1 : undefined,
      preProcessHtml: query.preProcessHtml ? 1 : undefined,
    })}`,
  );

export const patchMessage = (
  accountId: string,
  messageId: string,
  body: { addFlags?: string[]; removeFlags?: string[]; setFlags?: string[] },
): Promise<MessageView> =>
  api.patch<MessageView>(`${acc(accountId)}/messages/${encodeURIComponent(messageId)}`, body);

export const deleteMessage = (accountId: string, messageId: string): Promise<void> =>
  api.delete(`${acc(accountId)}/messages/${encodeURIComponent(messageId)}`);

export const moveMessage = (
  accountId: string,
  messageId: string,
  body: { mailboxId?: string; path?: string },
): Promise<void> =>
  api.post(`${acc(accountId)}/messages/${encodeURIComponent(messageId)}/move`, body);

export const batchMessages = (accountId: string, body: BatchInput): Promise<BatchResult> =>
  api.post<BatchResult>(`${acc(accountId)}/messages/batch`, body);

export type RawResult =
  | { ok: true; bytes: ArrayBuffer; contentType: string; source?: string }
  | { ok: false; missing: true }
  | { ok: false; missing: false; error: unknown };

export const getMessageRaw = async (
  accountId: string,
  messageId: string,
): Promise<RawResult> => {
  try {
    const res = await api.raw.get<ArrayBuffer>(
      `${acc(accountId)}/messages/${encodeURIComponent(messageId)}/raw`,
      { responseType: "arraybuffer" },
    );
    const hdrs = res.headers as unknown as Record<string, string> | undefined;
    const source = hdrs?.["eb-source"] ?? hdrs?.["EB-Source"];
    return {
      ok: true,
      bytes: res.data,
      contentType: "message/rfc822",
      source: typeof source === "string" ? source : undefined,
    };
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 404) {
      return { ok: false, missing: true };
    }
    return { ok: false, missing: false, error: err };
  }
};

export const downloadMessageRaw = async (
  accountId: string,
  messageId: string,
  filename: string,
): Promise<RawResult> => {
  const result = await getMessageRaw(accountId, messageId);
  if (!result.ok) return result;
  const blob = new Blob([result.bytes], { type: result.contentType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename.endsWith(".eml") ? filename : `${filename}.eml`;
  a.click();
  URL.revokeObjectURL(url);
  return result;
};

export const downloadAttachment = async (
  accountId: string,
  messageId: string,
  attachmentId: string,
  filename: string,
): Promise<RawResult> => {
  try {
    const res = await api.raw.get<ArrayBuffer>(
      `${acc(accountId)}/messages/${encodeURIComponent(messageId)}/attachments/${encodeURIComponent(attachmentId)}`,
      { responseType: "arraybuffer" },
    );
    const hdrs = res.headers as unknown as Record<string, string> | undefined;
    const blob = new Blob([res.data], {
      type: hdrs?.["content-type"] || "application/octet-stream",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename || "attachment";
    a.click();
    URL.revokeObjectURL(url);
    return { ok: true, bytes: res.data, contentType: blob.type };
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 404) {
      return { ok: false, missing: true };
    }
    return { ok: false, missing: false, error: err };
  }
};

export const listDrafts = (
  accountId: string,
  query: { page?: number; limit?: string | number; cursor?: string } = {},
): Promise<DraftListBody> =>
  api.get<DraftListBody>(
    `${acc(accountId)}/drafts${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      cursor: query.cursor,
    })}`,
  );

export const getDraft = (
  accountId: string,
  draftId: string,
  opts: { text?: boolean } = {},
): Promise<DraftView> =>
  api.get<DraftView>(
    `${acc(accountId)}/drafts/${encodeURIComponent(draftId)}${qs({
      text: opts.text ? 1 : undefined,
    })}`,
  );
