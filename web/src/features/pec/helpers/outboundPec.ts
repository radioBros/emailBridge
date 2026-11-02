import {
  mdiAlertCircle,
  mdiCheckCircle,
  mdiCloseCircle,
  mdiPackageDown,
  mdiProgressCheck,
} from "@mdi/js";
import type { StatusColor, StatusSpec } from "@/constants/Statuses";

export const PEC_OUTBOUND_STATES = [
  "accepted",
  "notAccepted",
  "delivered",
  "partiallyDelivered",
  "failed",
] as const;

export type PecOutboundState = (typeof PEC_OUTBOUND_STATES)[number];

export const PecOutboundStateSpecs = {
  accepted: { color: "info", icon: mdiProgressCheck },
  notAccepted: { color: "error", icon: mdiCloseCircle },
  delivered: { color: "success", icon: mdiCheckCircle },
  partiallyDelivered: { color: "warning", icon: mdiPackageDown },
  failed: { color: "error", icon: mdiCloseCircle },
} as const satisfies Record<PecOutboundState, StatusSpec>;

export const isPecOutboundState = (value: string): value is PecOutboundState =>
  (PEC_OUTBOUND_STATES as readonly string[]).includes(value);

export const pecOutboundStatusOf = (state: string): StatusSpec =>
  isPecOutboundState(state)
    ? PecOutboundStateSpecs[state]
    : { color: "grey" as StatusColor, icon: mdiAlertCircle };

export const pecOutboundLabelKey = (state: string): string =>
  isPecOutboundState(state) ? `pec.outbound_${state}` : "pec.outbound_unknown";

export const PEC_DELIVERY_STATES = ["delivered", "error", "warning"] as const;
export type PecDeliveryState = (typeof PEC_DELIVERY_STATES)[number];

export const pecDeliveryColor = (state: string): StatusColor => {
  if (state === "delivered") return "success";
  if (state === "warning") return "warning";
  if (state === "error") return "error";
  return "grey";
};

export const pecDeliveryLabelKey = (state: string): string => {
  if (state === "delivered" || state === "error" || state === "warning") {
    return `pec.delivery_${state}`;
  }
  return "pec.delivery_unknown";
};
