import { describe, expect, it } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { useNotifyStore } from "@/stores/notify";

describe("notify store", () => {
  it("pushes, persists errors, and dismisses", () => {
    setActivePinia(createPinia());
    const notify = useNotifyStore();
    const id = notify.push({ message: "Saved", color: "success" });
    expect(notify.items[0].timeout).toBe(4000);
    expect(notify.items[0].persistent).toBe(false);

    const errId = notify.push({ message: "Nope", color: "error", code: "x", traceId: "t1" });
    expect(notify.items.find((i) => i.id === errId)?.persistent).toBe(true);
    expect(notify.items.find((i) => i.id === errId)?.timeout).toBe(-1);

    notify.dismiss(id);
    expect(notify.items.some((i) => i.id === id)).toBe(false);
    notify.clear();
    expect(notify.items).toHaveLength(0);
  });
});
