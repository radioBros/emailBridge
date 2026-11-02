/** Organisation membership roles (S10 §4). */
export const ORG_ROLES = ["owner", "admin", "operator", "viewer"] as const;

export type OrgRole = (typeof ORG_ROLES)[number];

export const isOrgRole = (value: unknown): value is OrgRole =>
  typeof value === "string" && (ORG_ROLES as readonly string[]).includes(value);

export const roleColor = (role: string): string => {
  switch (role) {
    case "owner":
      return "primary";
    case "admin":
      return "info";
    case "operator":
      return "secondary";
    case "viewer":
      return "grey";
    default:
      return "grey";
  }
};

/** Members invite / role change / remove (S10: owner + admin; admin cannot touch owners). */
export const canWriteMembers = (role?: string | null): boolean =>
  role === "owner" || role === "admin";

export const canWriteApiKeys = (role?: string | null): boolean =>
  role === "owner" || role === "admin";

export const canReadAudit = (role?: string | null): boolean =>
  role === "owner" || role === "admin" || role === "operator";

/** Roles the caller may assign (admin cannot create/edit owners). */
export const assignableRoles = (callerRole?: string | null): OrgRole[] => {
  if (callerRole === "owner") return [...ORG_ROLES];
  if (callerRole === "admin") return ORG_ROLES.filter((r) => r !== "owner");
  return [];
};

export const canEditMember = (
  callerRole: string | null | undefined,
  targetRole: string,
): boolean => {
  if (!canWriteMembers(callerRole)) return false;
  if (callerRole === "admin" && targetRole === "owner") return false;
  return true;
};
