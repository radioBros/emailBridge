import type { AddressView, MessageView } from "@/api/messages";

export const formatAddresses = (addrs: AddressView[] | undefined): string => {
  if (!addrs?.length) return "—";
  return addrs
    .map((a) => (a.name ? `${a.name} <${a.address}>` : a.address))
    .join(", ");
};

/** List/from column: display name, else bare address. */
export const formatFrom = (addrs: AddressView[] | undefined): string => {
  const a = addrs?.[0];
  if (!a) return "—";
  return a.name?.trim() || a.address || "—";
};

/** Prefer Date header from the index, else IMAP internalDate. */
export const messageDateOf = (m: Pick<MessageView, "date" | "internalDate">): string =>
  m.date || m.internalDate;

export const formatBytes = (n: number | undefined): string => {
  if (n == null || Number.isNaN(n)) return "—";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
};

export const headerEntries = (
  headers: Record<string, string> | undefined,
): Array<{ name: string; value: string }> => {
  if (!headers) return [];
  return Object.entries(headers)
    .map(([name, value]) => ({ name, value }))
    .sort((a, b) => a.name.localeCompare(b.name));
};

/** Decode ArrayBuffer as UTF-8 text (lossy for binary MIME bodies). */
export const bytesToText = (buf: ArrayBuffer): string => {
  try {
    return new TextDecoder("utf-8", { fatal: false }).decode(buf);
  } catch {
    return "";
  }
};
