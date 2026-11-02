import {
  mdiAlertCircle,
  mdiCancel,
  mdiCheckCircle,
  mdiCircle,
  mdiCloseCircle,
  mdiCloudOffOutline,
  mdiHelpCircle,
  mdiLanDisconnect,
  mdiLoading,
  mdiPauseCircle,
  mdiPlayCircle,
  mdiSync,
} from "@mdi/js";

export type StatusColor =
  | "success"
  | "info"
  | "primary"
  | "warning"
  | "error"
  | "accent"
  | "grey";

export interface StatusSpec {
  color: StatusColor;
  icon: string;
}

/** Status → colour + icon (S13 §2.3). */
export const AccountStates = {
  connected: { color: "success", icon: mdiCheckCircle },
  syncing: { color: "info", icon: mdiSync },
  connecting: { color: "info", icon: mdiLoading },
  new: { color: "primary", icon: mdiPlayCircle },
  connectError: { color: "warning", icon: mdiAlertCircle },
  authError: { color: "error", icon: mdiCloseCircle },
  disabled: { color: "error", icon: mdiPauseCircle },
  disconnected: { color: "grey", icon: mdiLanDisconnect },
  deleting: { color: "grey", icon: mdiCloudOffOutline },
} as const satisfies Record<string, StatusSpec>;

export const OutboundStates = {
  sent: { color: "success", icon: mdiCheckCircle },
  queued: { color: "info", icon: mdiLoading },
  scheduled: { color: "info", icon: mdiCircle },
  sending: { color: "info", icon: mdiSync },
  captured: { color: "accent", icon: mdiCircle },
  cancelled: { color: "grey", icon: mdiCancel },
  failed: { color: "error", icon: mdiCloseCircle },
  outcomeUnknown: { color: "warning", icon: mdiHelpCircle },
} as const satisfies Record<string, StatusSpec>;

export const DeliveryStates = {
  succeeded: { color: "success", icon: mdiCheckCircle },
  pending: { color: "info", icon: mdiLoading },
  delivering: { color: "info", icon: mdiSync },
  retrying: { color: "warning", icon: mdiAlertCircle },
  failed: { color: "error", icon: mdiCloseCircle },
  deadLettered: { color: "error", icon: mdiCloseCircle },
  cancelled: { color: "grey", icon: mdiCancel },
} as const satisfies Record<string, StatusSpec>;

export const LogLevels = {
  trace: { color: "grey", icon: mdiCircle },
  debug: { color: "grey", icon: mdiCircle },
  info: { color: "info", icon: mdiCircle },
  warn: { color: "warning", icon: mdiAlertCircle },
  error: { color: "error", icon: mdiCloseCircle },
  fatal: { color: "error", icon: mdiCloseCircle },
} as const satisfies Record<string, StatusSpec>;

export const BreakerStates = {
  closed: { color: "success", icon: mdiCheckCircle },
  halfOpen: { color: "warning", icon: mdiAlertCircle },
  open: { color: "error", icon: mdiCloseCircle },
} as const satisfies Record<string, StatusSpec>;

export type AccountState = keyof typeof AccountStates;
export type OutboundState = keyof typeof OutboundStates;
export type DeliveryState = keyof typeof DeliveryStates;
export type LogLevel = keyof typeof LogLevels;
export type BreakerState = keyof typeof BreakerStates;
