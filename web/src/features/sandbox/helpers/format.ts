export const formatRecipients = (to: string[] | null | undefined): string => {
  if (!to?.length) return "—";
  if (to.length === 1) return to[0]!;
  return `${to[0]} +${to.length - 1}`;
};

/** datetime-local value → RFC 3339 UTC, or pass through if already ISO. */
export const toApiDate = (value: string): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};
