import { onMounted, onUnmounted, ref, shallowRef, type Ref } from "vue";

export type RealtimeStatus = "idle" | "connecting" | "open" | "closed";

export interface RealtimeMessage {
  id?: string;
  topic?: string;
  type?: string;
  data?: unknown;
  seq?: number;
  op?: string;
  code?: string;
}

export interface SubscribeArgs {
  id: string;
  topic: string;
  params?: Record<string, unknown>;
}

type MessageHandler = (msg: RealtimeMessage) => void;

const PING_MS = 25_000;
const BACKOFF_MIN = 1_000;
const BACKOFF_MAX = 30_000;

let shared: RealtimeClient | null = null;

class RealtimeClient {
  readonly status: Ref<RealtimeStatus> = ref("idle");
  private socket: WebSocket | null = null;
  private pingTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private backoff = BACKOFF_MIN;
  private intentionalClose = false;
  private everOpened = false;
  private readonly subs = new Map<string, SubscribeArgs>();
  private readonly handlers = new Set<MessageHandler>();
  private readonly reconnectHandlers = new Set<() => void>();

  connect(): void {
    if (typeof window === "undefined") return;
    if (
      this.socket &&
      (this.socket.readyState === WebSocket.OPEN ||
        this.socket.readyState === WebSocket.CONNECTING)
    ) {
      return;
    }
    this.intentionalClose = false;
    this.status.value = "connecting";
    const proto = window.location.protocol === "https:" ? "wss" : "ws";
    this.socket = new WebSocket(`${proto}://${window.location.host}/v1/admin/ws`);

    this.socket.addEventListener("open", () => {
      const isReconnect = this.everOpened;
      this.everOpened = true;
      this.status.value = "open";
      this.backoff = BACKOFF_MIN;
      for (const sub of this.subs.values()) {
        this.send({ op: "sub", id: sub.id, topic: sub.topic, params: sub.params ?? {} });
      }
      this.startPing();
      if (isReconnect) {
        for (const cb of this.reconnectHandlers) cb();
      }
    });

    this.socket.addEventListener("message", (ev) => {
      let msg: RealtimeMessage;
      try {
        msg = JSON.parse(String(ev.data)) as RealtimeMessage;
      } catch {
        return;
      }
      if (msg.op === "pong") return;
      for (const h of this.handlers) h(msg);
    });

    this.socket.addEventListener("close", () => {
      this.stopPing();
      this.status.value = "closed";
      this.socket = null;
      if (!this.intentionalClose) this.scheduleReconnect();
    });

    this.socket.addEventListener("error", () => {
      this.socket?.close();
    });
  }

  disconnect(): void {
    this.intentionalClose = true;
    this.stopPing();
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.socket?.close();
    this.socket = null;
    this.status.value = "closed";
  }

  subscribe(args: SubscribeArgs): void {
    this.subs.set(args.id, args);
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.send({ op: "sub", id: args.id, topic: args.topic, params: args.params ?? {} });
    } else {
      this.connect();
    }
  }

  unsubscribe(id: string): void {
    this.subs.delete(id);
    if (this.socket?.readyState === WebSocket.OPEN) {
      this.send({ op: "unsub", id });
    }
  }

  onMessage(handler: MessageHandler): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  onReconnect(handler: () => void): () => void {
    this.reconnectHandlers.add(handler);
    return () => this.reconnectHandlers.delete(handler);
  }

  /** Exposed for tests. */
  get activeSubscriptions(): SubscribeArgs[] {
    return [...this.subs.values()];
  }

  private send(payload: Record<string, unknown>): void {
    if (this.socket?.readyState !== WebSocket.OPEN) return;
    this.socket.send(JSON.stringify(payload));
  }

  private startPing(): void {
    this.stopPing();
    this.pingTimer = setInterval(() => this.send({ op: "ping" }), PING_MS);
  }

  private stopPing(): void {
    if (this.pingTimer) clearInterval(this.pingTimer);
    this.pingTimer = null;
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    const jitter = Math.random() * 0.3 * this.backoff;
    const delay = Math.min(BACKOFF_MAX, this.backoff + jitter);
    this.backoff = Math.min(BACKOFF_MAX, this.backoff * 2);
    this.reconnectTimer = setTimeout(() => this.connect(), delay);
  }
}

export const getRealtimeClient = (): RealtimeClient => {
  if (!shared) shared = new RealtimeClient();
  return shared;
};

/** Reset the singleton (tests only). */
export const resetRealtimeClient = (): void => {
  shared?.disconnect();
  shared = null;
};

/**
 * Subscribe while a component is mounted; reconnect with backoff; call
 * `onReconnect` so screens can refetch (S13 §6).
 */
export const useRealtime = (opts?: {
  subscribe?: SubscribeArgs | (() => SubscribeArgs | null);
  onMessage?: MessageHandler;
  onReconnect?: () => void;
}) => {
  const client = getRealtimeClient();
  const lastMessage = shallowRef<RealtimeMessage | null>(null);
  const status = client.status;

  onMounted(() => {
    client.connect();
    const sub =
      typeof opts?.subscribe === "function" ? opts.subscribe() : (opts?.subscribe ?? null);
    if (sub) client.subscribe(sub);

    const offMsg = client.onMessage((msg) => {
      lastMessage.value = msg;
      opts?.onMessage?.(msg);
    });
    const offRec = opts?.onReconnect ? client.onReconnect(opts.onReconnect) : () => undefined;

    onUnmounted(() => {
      if (sub) client.unsubscribe(sub.id);
      offMsg();
      offRec();
    });
  });

  return { status, lastMessage, client };
};
