import { api } from "@/api/client";

export interface MeMembership {
  organizationId: string;
  organizationName: string;
  role: string;
}

export interface MeUser {
  id: string;
  email: string;
  name: string;
  locale: string;
  instanceRole: string;
  status: string;
  lastLoginAt?: string;
}

/** GET /v1/me body (S2 / tenancy.MeOutput). */
export interface MeResponse {
  kind: string;
  user?: MeUser;
  apiKeyId?: string;
  scopes?: string[];
  activeOrganizationId?: string;
  role?: string;
  permissions: string[];
  memberships: MeMembership[];
  mfa: boolean;
}

export const getMe = (): Promise<MeResponse> => api.get<MeResponse>("/me");

/** Update display name and profile locale (theme stays in `/me/settings`). */
export const patchMe = (body: {
  name?: string;
  locale?: string;
}): Promise<MeResponse> => api.patch<MeResponse>("/me", body);

export interface UserSettingsBody {
  settings: {
    tables?: Record<string, unknown>;
    ui?: {
      theme?: string;
      locale?: string;
      density?: string;
    };
    [key: string]: unknown;
  };
  updatedAt?: string;
  version?: number;
}

export const getMySettings = (): Promise<UserSettingsBody> =>
  api.get<UserSettingsBody>("/me/settings");

/** Persist theme / locale under `settings.ui` (deep merge on the server). */
export const patchUiSettings = (ui: {
  theme?: string | null;
  locale?: string | null;
}): Promise<UserSettingsBody> => api.patch<UserSettingsBody>("/me/settings", { ui });
