import { api } from "@/api/client";

/** GET /v1/me/sessions item (P0.22 / brief 16b). */
export interface MeSession {
  id: string;
  current: boolean;
  createdAt: string;
  lastActiveAt: string;
  expiresAt: string;
  userAgent?: string;
  ip?: string;
  method?: string;
}

export interface MeSessionsList {
  data: MeSession[];
}

export const listMySessions = (): Promise<MeSessionsList> =>
  api.get<MeSessionsList>("/me/sessions");

export const revokeMySession = (sessionId: string): Promise<void> =>
  api.delete(`/me/sessions/${encodeURIComponent(sessionId)}`);

export const revokeOtherSessions = (): Promise<{ revoked: number }> =>
  api.post<{ revoked: number }>("/me/sessions/revoke-others");
