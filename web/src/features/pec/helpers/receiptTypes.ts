import {
  mdiAlertCircle,
  mdiBugOutline,
  mdiCheckCircle,
  mdiCloseCircle,
  mdiHandOkay,
  mdiPackageDown,
  mdiShieldAlert,
} from "@mdi/js";
import type { StatusColor, StatusSpec } from "@/constants/Statuses";

/** The seven receipt types shown in filters and chips (S17 §2). */
export const PEC_RECEIPT_TYPES = [
  "accettazione",
  "avvenuta-consegna",
  "errore-consegna",
  "preavviso-errore-consegna",
  "non-accettazione",
  "presa-in-carico",
  "rilevazione-virus",
] as const;

export type PecReceiptType = (typeof PEC_RECEIPT_TYPES)[number];

export const PecReceiptTypeSpecs = {
  accettazione: { color: "success", icon: mdiCheckCircle },
  "avvenuta-consegna": { color: "info", icon: mdiPackageDown },
  "errore-consegna": { color: "error", icon: mdiCloseCircle },
  "preavviso-errore-consegna": { color: "warning", icon: mdiAlertCircle },
  "non-accettazione": { color: "error", icon: mdiShieldAlert },
  "presa-in-carico": { color: "primary", icon: mdiHandOkay },
  "rilevazione-virus": { color: "error", icon: mdiBugOutline },
} as const satisfies Record<PecReceiptType, StatusSpec>;

export const isPecReceiptType = (value: string): value is PecReceiptType =>
  (PEC_RECEIPT_TYPES as readonly string[]).includes(value);

export const pecTypeStatusOf = (type: string): StatusSpec =>
  isPecReceiptType(type)
    ? PecReceiptTypeSpecs[type]
    : { color: "grey" as StatusColor, icon: mdiAlertCircle };

export const pecTypeLabelKey = (type: string): string =>
  isPecReceiptType(type) ? `pec.type_${type}` : "pec.type_unknown";
