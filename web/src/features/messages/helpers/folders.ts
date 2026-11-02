import {
  mdiAlertOctagon,
  mdiArchive,
  mdiEmailOutline,
  mdiFileDocumentOutline,
  mdiFolderOutline,
  mdiInbox,
  mdiSend,
  mdiTrashCanOutline,
} from "@mdi/js";
import type { MailboxView } from "@/api/messages";

/** Special-use → icon (RFC 6154 / IMAP SPECIAL-USE). */
export const specialUseIcon = (specialUse?: string): string => {
  const u = (specialUse ?? "").toLowerCase();
  if (u.includes("inbox")) return mdiInbox;
  if (u.includes("sent")) return mdiSend;
  if (u.includes("draft")) return mdiFileDocumentOutline;
  if (u.includes("trash") || u.includes("bin")) return mdiTrashCanOutline;
  if (u.includes("junk") || u.includes("spam")) return mdiAlertOctagon;
  if (u.includes("archive")) return mdiArchive;
  if (u.includes("all") || u.includes("flagged")) return mdiEmailOutline;
  return mdiFolderOutline;
};

export interface FolderNode {
  mailbox: MailboxView;
  label: string;
  children: FolderNode[];
  depth: number;
}

const leafName = (path: string, delimiter: string): string => {
  if (!delimiter) return path;
  const parts = path.split(delimiter);
  return parts[parts.length - 1] || path;
};

/** Build a tree from flat mailbox paths (delimiter-aware). */
export const buildFolderTree = (mailboxes: MailboxView[]): FolderNode[] => {
  const sorted = [...mailboxes].sort((a, b) => a.path.localeCompare(b.path));
  const roots: FolderNode[] = [];
  const byPath = new Map<string, FolderNode>();

  for (const mb of sorted) {
    const delim = mb.delimiter || "/";
    const node: FolderNode = {
      mailbox: mb,
      label: leafName(mb.path, delim),
      children: [],
      depth: 0,
    };
    byPath.set(mb.path, node);

    const parentPath =
      delim && mb.path.includes(delim)
        ? mb.path.slice(0, mb.path.lastIndexOf(delim))
        : "";
    const parent = parentPath ? byPath.get(parentPath) : undefined;
    if (parent) {
      node.depth = parent.depth + 1;
      parent.children.push(node);
    } else {
      roots.push(node);
    }
  }
  return roots;
};

/** Flatten tree for list UIs that prefer a single list with depth. */
export const flattenFolderTree = (nodes: FolderNode[]): FolderNode[] => {
  const out: FolderNode[] = [];
  const walk = (list: FolderNode[]): void => {
    for (const n of list) {
      out.push(n);
      if (n.children.length) walk(n.children);
    }
  };
  walk(nodes);
  return out;
};

export const draftsFolder = (mailboxes: MailboxView[]): MailboxView | undefined =>
  mailboxes.find((m) => (m.specialUse ?? "").toLowerCase().includes("draft")) ??
  mailboxes.find((m) => m.path.toLowerCase() === "drafts" || m.path.toLowerCase().endsWith("/drafts"));
