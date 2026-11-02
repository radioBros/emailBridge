/** Derive a URL-safe organisation slug from a display name ([a-z0-9-]{2,48}). */
export const slugifyOrgName = (name: string): string => {
  const base = name
    .normalize("NFKD")
    .replace(/[\u0300-\u036f]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .replace(/-+/g, "-");

  if (!base) return "";
  if (base.length === 1) return `${base}0`.slice(0, 48);
  return base.slice(0, 48);
};

export const isValidOrgSlug = (slug: string): boolean =>
  /^[a-z0-9-]{2,48}$/.test(slug);
