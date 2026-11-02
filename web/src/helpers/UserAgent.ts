export type UaBrowserKey =
  | "chrome"
  | "edge"
  | "firefox"
  | "safari"
  | "opera"
  | "generic";

export type UaOsKey =
  | "windows"
  | "macos"
  | "ios"
  | "android"
  | "linux"
  | "generic";

export interface ParsedUserAgent {
  empty: boolean;
  browserKey: UaBrowserKey;
  osKey: UaOsKey;
}

type Translate = (key: string, values?: Record<string, unknown>) => string;

/** Parse a UA into stable keys (iOS before macOS; CriOS / FxiOS before desktop Chrome/Firefox). */
export const parseUserAgent = (ua: string | null | undefined): ParsedUserAgent => {
  const raw = (ua ?? "").trim();
  if (!raw) {
    return { empty: true, browserKey: "generic", osKey: "generic" };
  }

  let browserKey: UaBrowserKey = "generic";
  if (/Edg\//i.test(raw)) browserKey = "edge";
  else if (/OPR\/|Opera/i.test(raw)) browserKey = "opera";
  else if (/CriOS\//i.test(raw)) browserKey = "chrome";
  else if (/FxiOS\//i.test(raw)) browserKey = "firefox";
  else if (/Chrome\//i.test(raw) && !/Chromium/i.test(raw)) browserKey = "chrome";
  else if (/Firefox\//i.test(raw)) browserKey = "firefox";
  else if (/Safari\//i.test(raw)) browserKey = "safari";

  let osKey: UaOsKey = "generic";
  if (/Windows NT/i.test(raw)) osKey = "windows";
  else if (/iPhone|iPad|iPod/i.test(raw)) osKey = "ios";
  else if (/Android/i.test(raw)) osKey = "android";
  else if (/Mac OS X|Macintosh/i.test(raw)) osKey = "macos";
  else if (/Linux/i.test(raw)) osKey = "linux";

  return { empty: false, browserKey, osKey };
};

/** Format a UA label via i18n (`me.ua_*` keys). */
export const describeUserAgent = (
  ua: string | null | undefined,
  t: Translate,
): string => {
  const parsed = parseUserAgent(ua);
  if (parsed.empty) return t("me.ua_unknown_device");
  const browser = t(`me.ua_browser_${parsed.browserKey}`);
  const os = t(`me.ua_os_${parsed.osKey}`);
  return t("me.ua_on", { browser, os });
};
