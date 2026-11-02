import { describe, expect, it } from "vitest";
import { chunkRows, mergePage, resolveGridColumns } from "@/helpers/CardList";
import {
  arrangeColumns,
  isActionKey,
  mergeColumnOrder,
  normalizeHeaders,
} from "@/helpers/TableColumns";
import { isOutdated, updateReason } from "@/helpers/UpdateCheck";
import { describeUserAgent, parseUserAgent } from "@/helpers/UserAgent";
import { takeInviteTokenFromHash } from "@/api/invitations";

const tEn = (key: string, values?: Record<string, unknown>): string => {
  const map: Record<string, string> = {
    "me.ua_unknown_device": "Unknown device",
    "me.ua_on": "{browser} on {os}",
    "me.ua_browser_chrome": "Chrome",
    "me.ua_browser_edge": "Edge",
    "me.ua_browser_firefox": "Firefox",
    "me.ua_browser_safari": "Safari",
    "me.ua_browser_opera": "Opera",
    "me.ua_browser_generic": "Browser",
    "me.ua_os_windows": "Windows",
    "me.ua_os_macos": "macOS",
    "me.ua_os_ios": "iOS",
    "me.ua_os_android": "Android",
    "me.ua_os_linux": "Linux",
    "me.ua_os_generic": "Unknown OS",
  };
  let out = map[key] ?? key;
  if (values) {
    for (const [k, v] of Object.entries(values)) {
      out = out.replace(`{${k}}`, String(v));
    }
  }
  return out;
};

describe("helpers", () => {
  it("CardList merge/chunk/grid", () => {
    const keyOf = (i: { id: string }) => i.id;
    expect(mergePage([], [{ id: "1" }], 1, 10, keyOf)).toEqual([{ id: "1" }]);
    expect(mergePage([{ id: "1" }], [{ id: "2" }], 2, 1, keyOf)).toEqual([
      { id: "1" },
      { id: "2" },
    ]);
    expect(chunkRows([{ id: "a" }, { id: "b" }, { id: "c" }], 2, keyOf)).toHaveLength(2);
    expect(resolveGridColumns({ xs: 1, md: 2 }, "md")).toBe(2);
    expect(resolveGridColumns({}, "lg")).toBe(1);
  });

  it("TableColumns invariants", () => {
    expect(isActionKey("_actions")).toBe(true);
    expect(isActionKey("name")).toBe(false);
    expect(mergeColumnOrder(["c", "a"], ["a", "b", "c"])).toEqual(["c", "a", "b"]);
    const headers = normalizeHeaders<{ key: string; title: string; forced?: boolean; sortable?: boolean }>([
      { key: "name", title: "Name" },
      { key: "actions", title: "…" },
    ]);
    expect(headers[1]?.forced).toBe(true);
    expect(headers[1]?.sortable).toBe(false);
    const arranged = arrangeColumns(headers, [{ key: "name" }], ["name"]);
    expect(arranged.map((h) => h.key)).toEqual(["name", "actions"]);
  });

  it("parseUserAgent / describeUserAgent cover browsers and OSes", () => {
    const cases: Array<[string, string, string]> = [
      [
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
        "chrome",
        "windows",
      ],
      [
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
        "safari",
        "macos",
      ],
      [
        "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1",
        "safari",
        "ios",
      ],
      [
        "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/120.0.0.0 Mobile/15E148 Safari/604.1",
        "chrome",
        "ios",
      ],
      [
        "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36",
        "chrome",
        "android",
      ],
      [
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36 Edg/120.0.0.0",
        "edge",
        "windows",
      ],
      [
        "Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0",
        "firefox",
        "linux",
      ],
    ];
    for (const [ua, browser, os] of cases) {
      expect(parseUserAgent(ua)).toEqual({ empty: false, browserKey: browser, osKey: os });
      expect(describeUserAgent(ua, tEn)).toMatch(/ on /);
    }
    expect(parseUserAgent("")).toEqual({
      empty: true,
      browserKey: "generic",
      osKey: "generic",
    });
    expect(describeUserAgent("", tEn)).toBe("Unknown device");
    expect(describeUserAgent(cases[0]![0], tEn)).toBe("Chrome on Windows");
    expect(describeUserAgent(cases[2]![0], tEn)).toBe("Safari on iOS");
    expect(describeUserAgent(cases[3]![0], tEn)).toBe("Chrome on iOS");
  });

  it("takeInviteTokenFromHash reads and clears the fragment", () => {
    window.history.pushState({}, "", "/accept-invite#tok%2F123");
    expect(takeInviteTokenFromHash()).toBe("tok/123");
    expect(window.location.hash).toBe("");
    expect(takeInviteTokenFromHash()).toBe("");
  });

  it("UpdateCheck updateReason", () => {
    expect(
      updateReason({ version: "1.0.0", sha: "aaa" }, { app_version: "1.0.0", git_sha: "aaa" }),
    ).toBeNull();
    expect(
      updateReason({ version: "1.0.0", sha: "aaa" }, { app_version: "1.0.0", git_sha: "bbb" }),
    ).toBe("sha");
    expect(updateReason({ version: "1.0.0", sha: "" }, { app_version: "2.0.0", git_sha: "" })).toBe(
      "version",
    );
    expect(isOutdated({ version: "1", sha: "a" }, null)).toBe(false);
  });
});
