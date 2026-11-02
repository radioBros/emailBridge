import { describe, expect, it } from "vitest";
import {
  FLAG_FLAGGED,
  FLAG_SEEN,
  hasFlag,
  isFlagged,
  isUnread,
  listFlagParam,
} from "@/features/messages/helpers/flags";
import {
  buildFolderTree,
  flattenFolderTree,
  specialUseIcon,
} from "@/features/messages/helpers/folders";
import {
  formatAddresses,
  formatBytes,
  formatFrom,
  headerEntries,
  messageDateOf,
} from "@/features/messages/helpers/format";
import { hasAttachmentFlag } from "@/features/messages/helpers/flags";
import type { MailboxView } from "@/api/messages";

describe("messages flags", () => {
  it("detects unread and flagged", () => {
    expect(isUnread([])).toBe(true);
    expect(isUnread([FLAG_SEEN])).toBe(false);
    expect(isFlagged([FLAG_FLAGGED])).toBe(true);
    expect(hasFlag(["\\seen"], FLAG_SEEN)).toBe(true);
  });

  it("maps filter to list flag param", () => {
    expect(listFlagParam("")).toBeUndefined();
    expect(listFlagParam("unread")).toBeUndefined();
    expect(listFlagParam("flagged")).toBe(FLAG_FLAGGED);
  });
});

describe("messages folders", () => {
  const boxes: MailboxView[] = [
    {
      id: "mbx_1",
      accountId: "acc_1",
      path: "INBOX",
      specialUse: "\\Inbox",
      uidValidity: 1,
      exists: 3,
      status: "active",
    },
    {
      id: "mbx_2",
      accountId: "acc_1",
      path: "INBOX/Work",
      delimiter: "/",
      uidValidity: 1,
      exists: 1,
      status: "active",
    },
    {
      id: "mbx_3",
      accountId: "acc_1",
      path: "Sent",
      specialUse: "\\Sent",
      uidValidity: 1,
      status: "active",
    },
  ];

  it("builds a nested tree and flattens with depth", () => {
    const tree = buildFolderTree(boxes);
    expect(tree).toHaveLength(2);
    const inbox = tree.find((n) => n.mailbox.id === "mbx_1");
    expect(inbox?.children).toHaveLength(1);
    expect(inbox?.children[0]?.label).toBe("Work");
    const flat = flattenFolderTree(tree);
    expect(flat.map((n) => n.mailbox.path)).toEqual(["INBOX", "INBOX/Work", "Sent"]);
    expect(flat[1]?.depth).toBe(1);
  });

  it("picks special-use icons", () => {
    expect(specialUseIcon("\\Inbox")).toBeTruthy();
    expect(specialUseIcon("\\Trash")).toBeTruthy();
    expect(specialUseIcon("\\Junk")).toBeTruthy();
    expect(specialUseIcon("\\Archive")).toBeTruthy();
    expect(specialUseIcon("\\Drafts")).toBeTruthy();
    expect(specialUseIcon("\\All")).toBeTruthy();
    expect(specialUseIcon(undefined)).toBeTruthy();
  });

  it("finds drafts folder", async () => {
    const { draftsFolder } = await import("@/features/messages/helpers/folders");
    expect(draftsFolder(boxes)?.id).toBeUndefined();
    expect(
      draftsFolder([
        ...boxes,
        {
          id: "mbx_d",
          accountId: "acc_1",
          path: "Drafts",
          specialUse: "\\Drafts",
          uidValidity: 1,
          status: "active",
        },
      ])?.id,
    ).toBe("mbx_d");
  });
});

describe("messages format", () => {
  it("formats addresses, from, date and sizes", () => {
    expect(formatAddresses(undefined)).toBe("—");
    expect(formatAddresses([{ address: "a@t" }])).toBe("a@t");
    expect(formatAddresses([{ name: "Ada", address: "a@t" }])).toBe("Ada <a@t>");
    expect(formatFrom([{ name: "Ada", address: "a@t" }])).toBe("Ada");
    expect(formatFrom([{ address: "a@t" }])).toBe("a@t");
    expect(formatFrom(undefined)).toBe("—");
    expect(messageDateOf({ date: "2026-01-02T00:00:00Z", internalDate: "2026-01-01T00:00:00Z" })).toBe(
      "2026-01-02T00:00:00Z",
    );
    expect(messageDateOf({ internalDate: "2026-01-01T00:00:00Z" })).toBe("2026-01-01T00:00:00Z");
    expect(hasAttachmentFlag(["$HasAttachment"])).toBe(true);
    expect(hasAttachmentFlag(["\\Seen"])).toBe(false);
    expect(formatBytes(500)).toBe("500 B");
    expect(formatBytes(2048)).toBe("2.0 KB");
    expect(formatBytes(2 * 1024 * 1024)).toBe("2.0 MB");
    expect(formatBytes(undefined)).toBe("—");
  });

  it("sorts header entries and decodes bytes", async () => {
    expect(headerEntries({ Subject: "Hi", From: "a" }).map((h) => h.name)).toEqual([
      "From",
      "Subject",
    ]);
    expect(headerEntries(undefined)).toEqual([]);
    const { bytesToText } = await import("@/features/messages/helpers/format");
    expect(bytesToText(new TextEncoder().encode("hi").buffer)).toBe("hi");
  });
});
