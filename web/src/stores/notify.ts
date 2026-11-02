import { defineStore } from "pinia";
import { ref } from "vue";
import type { ActionColor } from "@/constants/Actions";
import type { StatusColor } from "@/constants/Statuses";

export type NotifyColor = ActionColor | StatusColor | "primary" | "secondary";

export interface NotifyItem {
  id: number;
  message: string;
  color: NotifyColor;
  timeout: number;
  persistent: boolean;
  code?: string;
  traceId?: string;
}

let nextId = 1;

export const useNotifyStore = defineStore("notify", () => {
  const items = ref<NotifyItem[]>([]);

  const push = (opts: {
    message: string;
    color?: NotifyColor;
    timeout?: number;
    persistent?: boolean;
    code?: string;
    traceId?: string;
  }): number => {
    const id = nextId++;
    const persistent = Boolean(opts.persistent || opts.color === "error");
    items.value = [
      ...items.value,
      {
        id,
        message: opts.message,
        color: opts.color ?? "primary",
        timeout: persistent ? -1 : (opts.timeout ?? 4000),
        persistent,
        code: opts.code,
        traceId: opts.traceId,
      },
    ];
    return id;
  };

  const dismiss = (id: number): void => {
    items.value = items.value.filter((n) => n.id !== id);
  };

  const clear = (): void => {
    items.value = [];
  };

  return { items, push, dismiss, clear };
});
