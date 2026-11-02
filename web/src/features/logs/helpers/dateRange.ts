/** datetime / ISO → RFC 3339 UTC for log API bounds. */
export const toApiDate = (value: string): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};
