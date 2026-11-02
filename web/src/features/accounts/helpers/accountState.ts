import { AccountStates, type AccountState, type StatusSpec } from "@/constants/Statuses";

export const ACCOUNT_STATE_KEYS = Object.keys(AccountStates) as AccountState[];

export const isAccountState = (value: string): value is AccountState => value in AccountStates;

export const accountStatusOf = (state: string): StatusSpec =>
  isAccountState(state) ? AccountStates[state] : AccountStates.connectError;

/** Resume is only meaningful when the account is disabled. */
export const canResume = (state: string): boolean => state === "disabled";

export const canSync = (state: string): boolean =>
  state !== "deleting" && state !== "disabled";

export const canReconnect = (state: string): boolean =>
  state !== "deleting" && state !== "disabled" && state !== "new";

export const canEdit = (state: string): boolean => state !== "deleting";

export const canDelete = (state: string): boolean => state !== "deleting";

export const TLS_OPTIONS = ["implicit", "starttls", "none"] as const;
export type TlsMode = (typeof TLS_OPTIONS)[number];

export const SEND_POLICIES = ["standard", "atMostOnce"] as const;
export type SendPolicy = (typeof SEND_POLICIES)[number];

/** Account-scope switches shown on the Settings / Protocol logs panels. */
export const ACCOUNT_SWITCH_KEYS = [
  "protocolLogs.enabled",
  "protocolLogs.rawDebug",
  "sandbox.enabled",
  "inbound.authResults",
  "inbound.archiveRaw",
  "accounts.ignoreCertErrors",
  "accounts.authServer",
  "oauth.tokenExport",
  "search.fullText",
  "ai.embeddings",
] as const;

export type AccountSwitchKey = (typeof ACCOUNT_SWITCH_KEYS)[number];

/**
 * Switches whose organisation value is the master and an account can only opt out (G3 full-text, G1
 * semantic search): an account that has not set them follows the organisation, so "not set" shows as on.
 */
export const ORG_MASTER_SWITCH_KEYS: readonly string[] = ["search.fullText", "ai.embeddings"];

export const isOrgMasterSwitch = (key: string): boolean => ORG_MASTER_SWITCH_KEYS.includes(key);

export const switchOn = (
  switches: Record<string, unknown> | undefined,
  key: string,
): boolean => (isOrgMasterSwitch(key) ? switches?.[key] !== false : switches?.[key] === true);
