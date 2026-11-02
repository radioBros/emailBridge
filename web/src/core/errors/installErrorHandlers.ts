import type { App } from "vue";
import i18n from "@/app/i18n";
import { useNotifyStore } from "@/stores/notify";
import { reportClientError } from "@/core/errors/reportClientError";

const DEDUPE_MS = 2000;
let lastKey = "";
let lastAt = 0;

const fingerprint = (err: unknown): string => {
  if (err instanceof Error) return `${err.name}:${err.message}:${err.stack?.slice(0, 200) ?? ""}`;
  return String(err);
};

const shouldNotify = (key: string): boolean => {
  const now = Date.now();
  if (key === lastKey && now - lastAt < DEDUPE_MS) return false;
  lastKey = key;
  lastAt = now;
  return true;
};

const messageOf = (err: unknown): string => {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === "string" && err) return err;
  try {
    return String(i18n.global.t("shell.unexpected_error"));
  } catch {
    return "Something went wrong.";
  }
};

const surface = (err: unknown, context: string): void => {
  reportClientError(err, context);
  const key = fingerprint(err);
  if (!shouldNotify(key)) return;
  try {
    useNotifyStore().push({ message: messageOf(err), color: "error" });
  } catch {
    /* Pinia not ready — report only */
  }
};

/** Vue + window safety net: notify (deduped) + report. Call after `app.use(pinia)`. */
export const installErrorHandlers = (app: App): void => {
  app.config.errorHandler = (err, _instance, info) => {
    surface(err, `vue:${info || "errorHandler"}`);
  };

  if (typeof window === "undefined") return;
  window.addEventListener("unhandledrejection", (ev) => {
    surface(ev.reason ?? ev, "unhandledrejection");
  });
};

/** Test helper — reset dedupe window between cases. */
export const __resetErrorHandlerDedupe = (): void => {
  lastKey = "";
  lastAt = 0;
};
