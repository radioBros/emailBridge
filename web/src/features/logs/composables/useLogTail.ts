import { getCurrentInstance, onUnmounted, ref, type Ref } from "vue";
import type { LogRecord } from "@/api/logs";
import { logsStreamPath, type LogLevel } from "@/api/logs";
import { useSessionStore } from "@/stores/session";

const TAIL_CAP = 500;

export interface TailFilter {
  category?: string[];
  level?: LogLevel;
  accountId?: string;
  q?: string;
}

/**
 * SSE live tail over GET /v1/logs/stream (OpenAPI). Pause on hover; cap 500 rows.
 */
export const useLogTail = (rows: Ref<LogRecord[]>) => {
  const live = ref(false);
  const paused = ref(false);
  const error = ref<string | null>(null);
  let abort: AbortController | null = null;
  const buffer: LogRecord[] = [];
  const hasOwner = Boolean(getCurrentInstance());

  const flushBuffer = (): void => {
    if (!buffer.length) return;
    const incoming = buffer.splice(0, buffer.length);
    const merged = [...incoming, ...rows.value];
    const seen = new Set<string>();
    const unique: LogRecord[] = [];
    for (const r of merged) {
      if (seen.has(r.id)) continue;
      seen.add(r.id);
      unique.push(r);
      if (unique.length >= TAIL_CAP) break;
    }
    rows.value = unique;
  };

  const stop = (): void => {
    live.value = false;
    abort?.abort();
    abort = null;
  };

  const start = async (filter: TailFilter): Promise<void> => {
    stop();
    live.value = true;
    error.value = null;
    abort = new AbortController();
    const session = useSessionStore();
    const headers: Record<string, string> = {
      Accept: "text/event-stream",
      "X-Requested-With": "EmailBridge",
    };
    const org = session.organizationId ?? session.me?.activeOrganizationId;
    if (org) headers["EB-Organization"] = org;

    try {
      const res = await fetch(logsStreamPath(filter), {
        credentials: "include",
        headers,
        signal: abort.signal,
      });
      if (!res.ok || !res.body) {
        error.value = `stream ${res.status}`;
        stop();
        return;
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let leftover = "";
      while (live.value) {
        const { done, value } = await reader.read();
        if (done) break;
        leftover += decoder.decode(value, { stream: true });
        const parts = leftover.split("\n\n");
        leftover = parts.pop() ?? "";
        for (const block of parts) {
          const dataLine = block
            .split("\n")
            .find((l) => l.startsWith("data:"));
          if (!dataLine) continue;
          const raw = dataLine.slice(5).trim();
          if (!raw || raw === "[DONE]") continue;
          try {
            const record = JSON.parse(raw) as LogRecord;
            if (!record?.id) continue;
            if (paused.value) buffer.push(record);
            else {
              buffer.push(record);
              flushBuffer();
            }
          } catch {
            /* ignore malformed */
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        error.value = (err as Error).message || "stream error";
      }
    } finally {
      live.value = false;
    }
  };

  const setPaused = (value: boolean): void => {
    paused.value = value;
    if (!value) flushBuffer();
  };

  if (hasOwner) onUnmounted(() => stop());

  return { live, paused, error, start, stop, setPaused, cap: TAIL_CAP };
};
