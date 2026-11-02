import { api, ApiProblemError, type ApiProblem } from "@/api/client";
import { auth } from "@/api/auth";

/**
 * Invitation acceptance (S10 §2.4 / P0.15b).
 * Mail link: `<PublicURL>/accept-invite#<token>` (fragment never hits the server).
 * - `POST /v1/invitations/lookup` `{ token }`
 * - `POST /v1/invitations/accept` `{ token, password?, name? }`
 */

export interface InvitePreview {
  email: string;
  organizationName: string;
  inviterName?: string;
  existingUser: boolean;
  expiresAt?: string;
}

export interface AcceptInviteInput {
  token: string;
  password?: string;
  name?: string;
}

/** Read the invite token from the URL fragment and clear it from the address bar. */
export const takeInviteTokenFromHash = (): string => {
  if (typeof window === "undefined") return "";
  const raw = window.location.hash.replace(/^#/, "").trim();
  if (!raw) return "";
  const path = `${window.location.pathname}${window.location.search}`;
  window.history.replaceState(null, "", path);
  return decodeURIComponent(raw);
};

export const lookupInvite = async (token: string): Promise<InvitePreview> => {
  return api.post<InvitePreview>("/invitations/lookup", { token });
};

/**
 * Accept an invitation. New users pass `password` (+ optional `name`);
 * existing users must already be signed in (no password).
 */
export const acceptInvite = async (input: AcceptInviteInput): Promise<void> => {
  const body: Record<string, string> = { token: input.token };
  if (input.password) body.password = input.password;
  if (input.name) body.name = input.name;
  await api.post("/invitations/accept", body);
  await auth.getSession();
};

/** An expired invitation: `410 invitation_expired` (legacy spellings kept for older servers). */
export const isExpiredInviteCode = (code: string): boolean =>
  code === "invitation_expired" || code === "invite_expired" || code === "gone";

export const inviteErrorCode = (err: unknown): string => {
  if (err instanceof ApiProblemError) return err.problem.code;
  const p = err as ApiProblem | null;
  if (p && typeof p === "object" && typeof p.code === "string") return p.code;
  return "internal_error";
};
