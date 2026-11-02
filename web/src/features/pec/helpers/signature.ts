import { mdiCheckDecagram, mdiShieldAlert, mdiShieldCheck, mdiShieldOff } from "@mdi/js";
import type { Signature } from "@/api/pec";
import type { StatusColor } from "@/constants/Statuses";

export type SignatureBadgeKind = "validTrusted" | "validUntrusted" | "invalid" | "unknown";

export interface SignatureBadge {
  kind: SignatureBadgeKind;
  color: StatusColor;
  icon: string;
  labelKey: string;
  reason?: string;
}

export const signatureBadgeOf = (sig: Signature | null | undefined): SignatureBadge => {
  if (!sig) {
    return {
      kind: "unknown",
      color: "grey",
      icon: mdiShieldOff,
      labelKey: "pec.sig_unknown",
    };
  }
  if (!sig.valid) {
    return {
      kind: "invalid",
      color: "error",
      icon: mdiShieldAlert,
      labelKey: "pec.sig_invalid",
      reason: sig.error,
    };
  }
  if (sig.trusted === true) {
    return {
      kind: "validTrusted",
      color: "success",
      icon: mdiCheckDecagram,
      labelKey: "pec.sig_valid_trusted",
    };
  }
  return {
    kind: "validUntrusted",
    color: "warning",
    icon: mdiShieldCheck,
    labelKey: "pec.sig_valid_untrusted",
    reason: sig.trusted === null ? "pec.sig_trust_unknown" : undefined,
  };
};
