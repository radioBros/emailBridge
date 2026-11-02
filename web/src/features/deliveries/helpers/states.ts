import { DeliveryStates, type DeliveryState, type StatusSpec } from "@/constants/Statuses";

export const DELIVERY_STATE_KEYS = Object.keys(DeliveryStates) as DeliveryState[];

export const isDeliveryState = (value: string): value is DeliveryState => value in DeliveryStates;

export const deliveryStatusOf = (state: string): StatusSpec =>
  isDeliveryState(state) ? DeliveryStates[state] : DeliveryStates.failed;

export const FAILED_QUICK = "failed,deadLettered";

export const lastStatusCode = (attempts: { statusCode?: number }[] | null | undefined): number | null => {
  if (!attempts?.length) return null;
  const last = attempts[attempts.length - 1];
  return last?.statusCode ?? null;
};

export const toApiDate = (value: string): string | undefined => {
  if (!value) return undefined;
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return value;
  return d.toISOString();
};
