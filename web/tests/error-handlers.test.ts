import { beforeEach, describe, expect, it, vi } from "vitest";
import { createApp, h } from "vue";
import { createPinia, setActivePinia } from "pinia";
import {
  __resetErrorHandlerDedupe,
  installErrorHandlers,
} from "@/core/errors/installErrorHandlers";
import { useNotifyStore } from "@/stores/notify";

vi.mock("@/app/i18n", () => ({
  default: {
    global: {
      t: (k: string) => (k === "shell.unexpected_error" ? "Something went wrong." : k),
    },
  },
}));

const makeApp = () => {
  const pinia = createPinia();
  setActivePinia(pinia);
  const app = createApp({ render: () => h("div") });
  app.use(pinia);
  installErrorHandlers(app);
  return app;
};

describe("installErrorHandlers", () => {
  beforeEach(() => {
    __resetErrorHandlerDedupe();
    vi.spyOn(console, "error").mockImplementation(() => undefined);
  });

  it("notifies on Vue errorHandler and dedupes identical errors", () => {
    const app = makeApp();
    const notify = useNotifyStore();
    const err = new Error("boom");
    app.config.errorHandler?.(err, null, "render");
    app.config.errorHandler?.(err, null, "render");

    expect(notify.items).toHaveLength(1);
    expect(notify.items[0].message).toBe("boom");
    expect(notify.items[0].color).toBe("error");
    expect(console.error).toHaveBeenCalled();
  });

  it("surfaces unhandledrejection", () => {
    makeApp();
    const notify = useNotifyStore();
    // happy-dom has no PromiseRejectionEvent — synthesize the shape the handler reads.
    const ev = new Event("unhandledrejection") as Event & { reason: Error };
    ev.reason = new Error("rejected");
    window.dispatchEvent(ev);

    expect(notify.items.some((n) => n.message === "rejected")).toBe(true);
  });
});
