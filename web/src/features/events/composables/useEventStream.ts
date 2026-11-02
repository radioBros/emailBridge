import { getCurrentInstance, onUnmounted, ref, type Ref } from "vue";
import type { EbEvent } from "@/api/events";
import { eventsStreamPath } from "@/api/events";
import { useSessionStore } from "@/stores/session";

const LIVE_CAP = 200;

export interface StreamFilter {
  type?: string;
  accountId?: string;
  lastEventId?: string;
}

/**
 * SSE live tail over GET /v1/events/stream. Cap 200 rows; pause when the tab is hidden.
 */
export const useEventStream = (rows: Ref<EbEvent[]>) => {
  const live = ref(false);
  const reconnecting = ref(false);
  const error = ref<string | null>(null);
  let abort: AbortController | null = null;
  let visibilityHandler: (() => void) | null = null;
  const hasOwner = Boolean(getCurrentInstance());
  let currentFilter: StreamFilter = {};

  const prepend = (incoming: EbEvent[]): void => {
    if (!incoming.length) return;
    const seen = new Set<string>();
    const unique: EbEvent[] = [];
    for (const r of [...incoming, ...rows.value]) {
      if (seen.has(r.id)) continue;
      seen.add(r.id);
      unique.push(r);
      if (unique.length >= LIVE_CAP) break;
    }
    rows.value = unique;
  };

  const stop = (): void => {
    live.value = false;
    reconnecting.value = false;
    abort?.abort();
    abort = null;
    if (visibilityHandler) {
      document.removeEventListener("visibilitychange", visibilityHandler);
      visibilityHandler = null;
    }
  };

  const connect = async (filter: StreamFilter): Promise<void> => {
    abort?.abort();
    abort = new AbortController();
    const session = useSessionStore();
    const headers: Record<string, string> = {
      Accept: "text/event-stream",
      "X-Requested-With": "EmailBridge",
    };
    const org = session.organizationId ?? session.me?.activeOrganizationId;
    if (org) headers["EB-Organization"] = org;
    const newest = rows.value[0]?.id;
    const path = eventsStreamPath({
      ...filter,
      lastEventId: filter.lastEventId ?? newest,
    });

    try {
      const res = await fetch(path, {
        credentials: "include",
        headers,
        signal: abort.signal,
      });
      if (!res.ok || !res.body) {
        error.value = `stream ${res.status}`;
        live.value = false;
        reconnecting.value = false;
        return;
      }
      live.value = true;
      reconnecting.value = false;
      error.value = null;
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let leftover = "";
      while (live.value) {
        if (document.hidden) {
          await new Promise<void>((resolve) => {
            const onVis = (): void => {
              if (!document.hidden) {
                document.removeEventListener("visibilitychange", onVis);
                resolve();
              }
            };
            document.addEventListener("visibilitychange", onVis);
          });
          if (!live.value) break;
        }
        const { done, value } = await reader.read();
        if (done) break;
        leftover += decoder.decode(value, { stream: true });
        const parts = leftover.split("\n\n");
        leftover = parts.pop() ?? "";
        const batch: EbEvent[] = [];
        for (const block of parts) {
          const dataLine = block.split("\n").find((l) => l.startsWith("data:"));
          if (!dataLine) continue;
          const raw = dataLine.slice(5).trim();
          if (!raw) continue;
          try {
            const record = JSON.parse(raw) as EbEvent;
            if (record?.id) batch.push(record);
          } catch {
            /* ignore malformed */
          }
        }
        if (batch.length) prepend(batch);
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        error.value = (err as Error).message || "stream error";
      }
    } finally {
      if (live.value) {
        reconnecting.value = true;
        live.value = false;
      }
    }
  };

  const start = async (filter: StreamFilter): Promise<void> => {
    stop();
    currentFilter = { ...filter };
    live.value = true;
    error.value = null;
    visibilityHandler = () => {
      /* pause handled inside connect loop */
    };
    document.addEventListener("visibilitychange", visibilityHandler);
    await connect(currentFilter);
  };

  if (hasOwner) onUnmounted(() => stop());

  return { live, reconnecting, error, start, stop, cap: LIVE_CAP };
};
