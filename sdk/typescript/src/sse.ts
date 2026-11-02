import type { Client } from "./client.js";

export type SSEEvent = {
  id?: string;
  event?: string;
  data: string;
};

export type StreamEventsOptions = {
  types?: string;
  accountId?: string;
  lastEventId?: string;
  signal?: AbortSignal;
  /** Called with each parsed event. */
  onEvent: (ev: SSEEvent) => void | Promise<void>;
  fetch?: typeof fetch;
};

const parseBlock = (block: string): SSEEvent | null => {
  let id: string | undefined;
  let event: string | undefined;
  const dataLines: string[] = [];
  for (const line of block.split("\n")) {
    if (!line || line.startsWith(":")) continue;
    const i = line.indexOf(":");
    const field = i < 0 ? line : line.slice(0, i);
    let value = i < 0 ? "" : line.slice(i + 1);
    if (value.startsWith(" ")) value = value.slice(1);
    if (field === "id") id = value;
    else if (field === "event") event = value;
    else if (field === "data") dataLines.push(value);
  }
  if (!id && !event && dataLines.length === 0) return null;
  return { id, event, data: dataLines.join("\n") };
};

/**
 * Consume `GET /v1/events/stream` as SSE. Sends `Last-Event-ID` when `lastEventId` is set.
 * Resolves when the stream ends or `signal` aborts.
 */
export const streamEvents = async (client: Client, opts: StreamEventsOptions): Promise<void> => {
  const fetchImpl = opts.fetch ?? fetch.bind(globalThis);
  const url = new URL("v1/events/stream", client.baseUrl + "/");
  if (opts.types) url.searchParams.set("types", opts.types);
  if (opts.accountId) url.searchParams.set("accountId", opts.accountId);

  const headers: Record<string, string> = {
    Accept: "text/event-stream",
    Authorization: `Bearer ${client.apiKey}`,
  };
  if (client.organizationId) headers["EB-Organization"] = client.organizationId;
  if (opts.lastEventId) headers["Last-Event-ID"] = opts.lastEventId;

  const res = await fetchImpl(url.toString(), { headers, signal: opts.signal });
  if (!res.ok || !res.body) {
    const text = await res.text().catch(() => "");
    throw new Error(`SSE stream failed: ${res.status} ${text}`);
  }

  const reader = res.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let sep: number;
    while ((sep = buf.indexOf("\n\n")) >= 0) {
      const block = buf.slice(0, sep);
      buf = buf.slice(sep + 2);
      const ev = parseBlock(block.replace(/\r/g, ""));
      if (ev) await opts.onEvent(ev);
    }
  }
};
