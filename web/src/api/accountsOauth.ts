import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type AccountOAuthView = components["schemas"]["AccountOAuthView"];
export type OauthTokenExport = components["schemas"]["TokenExport"];
export type ServerSignature = components["schemas"]["ServerSignature"];
export type ServerSignatures = components["schemas"]["ServerSignatures"];
export type StartOauthResult = components["schemas"]["StartResult"];

export const getAccountOauth = (accountId: string): Promise<AccountOAuthView> =>
  api.get<AccountOAuthView>(`/accounts/${encodeURIComponent(accountId)}/oauth`);

export const startAccountOauth = (
  accountId: string,
  appId: string,
): Promise<StartOauthResult> =>
  api.post<StartOauthResult>(`/accounts/${encodeURIComponent(accountId)}/oauth/start`, {
    appId,
  });

export const disconnectAccountOauth = (accountId: string): Promise<void> =>
  api.delete(`/accounts/${encodeURIComponent(accountId)}/oauth`);

export const exportAccountOauthToken = (accountId: string): Promise<OauthTokenExport> =>
  api.get<OauthTokenExport>(`/accounts/${encodeURIComponent(accountId)}/oauth-token`);

export const listAccountServerSignatures = (accountId: string): Promise<ServerSignatures> =>
  api.get<ServerSignatures>(
    `/accounts/${encodeURIComponent(accountId)}/server-signatures`,
  );
