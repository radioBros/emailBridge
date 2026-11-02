import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type SettingsBody = components["schemas"]["SettingsBody"];
export type SettingValue = components["schemas"]["SettingValue"];
export type FeatureSwitchBody = components["schemas"]["FeatureSwitchBody"];
export type SwitchesOutBody = components["schemas"]["SwitchesOutBody"];

export type SettingsScope = "organization" | "global";

export interface SettingsResult {
  body: SettingsBody;
  etag: string | null;
}

const etagOf = (headers: Headers | Record<string, string> | undefined): string | null => {
  if (!headers) return null;
  if (headers instanceof Headers) return headers.get("ETag") ?? headers.get("etag");
  return headers.ETag ?? headers.etag ?? null;
};

/** GET /v1/settings — effective values for the caller's organisation. */
export const getSettings = async (): Promise<SettingsResult> => {
  const res = await api.raw.get<SettingsBody>("/settings");
  return { body: res.data, etag: etagOf(res.headers as Headers) };
};

/** PATCH /v1/settings?scope=… with optional If-Match. */
export const patchSettings = async (
  scope: SettingsScope,
  patch: Record<string, unknown>,
  ifMatch?: string | null,
): Promise<SettingsResult> => {
  const res = await api.raw.patch<SettingsBody>("/settings", patch, {
    params: { scope },
    headers: ifMatch ? { "If-Match": ifMatch } : undefined,
  });
  return { body: res.data, etag: etagOf(res.headers as Headers) };
};

/** GET /v1/feature-switches */
export const listFeatureSwitches = (): Promise<SwitchesOutBody> =>
  api.get<SwitchesOutBody>("/feature-switches");
