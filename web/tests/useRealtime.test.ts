import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getRealtimeClient, resetRealtimeClient } from "@/composables/useRealtime";

class MockWebSocket {
  static OPEN = 1;
  static CONNECTING = 0;
  static CLOSED = 3;
  static instances: MockWebSocket[] = [];

  readyState = MockWebSocket.CONNECTING;
  url: string;
  sent: string[] = [];
  private listeners = new Map<string, Set<(ev: Event) => void>>();

  constructor(url: string) {
    this.url = url;
    MockWebSocket.instances.push(this);
    queueMicrotask(() => this.open());
  }

  addEventListener(type: string, fn: (ev: Event) => void): void {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set());
    this.listeners.get(type)!.add(fn);
  }

  send(data: string): void {
    this.sent.push(data);
  }

  close(): void {
    this.readyState = MockWebSocket.CLOSED;
    this.emit("close", new Event("close"));
  }

  open(): void {
    this.readyState = MockWebSocket.OPEN;
    this.emit("open", new Event("open"));
  }

  private emit(type: string, ev: Event): void {
    for (const fn of this.listeners.get(type) ?? []) fn(ev);
  }
}

describe("useRealtime / RealtimeClient", () => {
  beforeEach(() => {
    resetRealtimeClient();
    MockWebSocket.instances = [];
    vi.stubGlobal("WebSocket", MockWebSocket as unknown as typeof WebSocket);
    vi.useFakeTimers();
  });

  afterEach(() => {
    resetRealtimeClient();
    vi.clearAllTimers();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("subscribes and resubscribes after reconnect", async () => {
    const client = getRealtimeClient();
    const onReconnect = vi.fn();
    client.onReconnect(onReconnect);
    client.subscribe({ id: "s1", topic: "dashboard.counters" });

    await vi.advanceTimersByTimeAsync(0);
    const first = MockWebSocket.instances[0];
    expect(first.url).toContain("/v1/admin/ws");
    expect(first.sent.some((s) => s.includes('"op":"sub"'))).toBe(true);
    expect(onReconnect).not.toHaveBeenCalled();

    first.close();
    await vi.advanceTimersByTimeAsync(2000);
    expect(MockWebSocket.instances.length).toBeGreaterThan(1);
    const second = MockWebSocket.instances.at(-1)!;
    await vi.advanceTimersByTimeAsync(0);
    expect(second.sent.some((s) => s.includes("dashboard.counters"))).toBe(true);
    expect(onReconnect).toHaveBeenCalled();
  });

  it("backs off between reconnects", async () => {
    const client = getRealtimeClient();
    client.connect();
    await vi.advanceTimersByTimeAsync(0);
    MockWebSocket.instances[0].close();

    const before = MockWebSocket.instances.length;
    await vi.advanceTimersByTimeAsync(500);
    expect(MockWebSocket.instances.length).toBe(before);
    await vi.advanceTimersByTimeAsync(2000);
    expect(MockWebSocket.instances.length).toBeGreaterThan(before);
  });
});
