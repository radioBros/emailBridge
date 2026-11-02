import { describe, expect, it } from "vitest";
import {
  pecDeliveryColor,
  pecDeliveryLabelKey,
  pecOutboundLabelKey,
  pecOutboundStatusOf,
} from "@/features/pec/helpers/outboundPec";
import {
  applyPecEnabled,
  applyPecInboxOnly,
  applyPecSendPolicy,
  joinPaths,
} from "@/features/pec/helpers/presets";
import {
  isPecReceiptType,
  PEC_RECEIPT_TYPES,
  pecTypeLabelKey,
  pecTypeStatusOf,
} from "@/features/pec/helpers/receiptTypes";
import { signatureBadgeOf } from "@/features/pec/helpers/signature";

describe("pec helpers", () => {
  it("lists seven receipt types", () => {
    expect(PEC_RECEIPT_TYPES).toHaveLength(7);
    expect(isPecReceiptType("accettazione")).toBe(true);
    expect(isPecReceiptType("nope")).toBe(false);
  });

  it("maps type colours and labels", () => {
    expect(pecTypeStatusOf("accettazione").color).toBe("success");
    expect(pecTypeStatusOf("errore-consegna").color).toBe("error");
    expect(pecTypeLabelKey("avvenuta-consegna")).toBe("pec.type_avvenuta-consegna");
    expect(pecTypeLabelKey("weird")).toBe("pec.type_unknown");
  });

  it("builds signature badges", () => {
    expect(signatureBadgeOf({ valid: true, trusted: true }).kind).toBe("validTrusted");
    expect(signatureBadgeOf({ valid: true, trusted: false }).kind).toBe("validUntrusted");
    expect(signatureBadgeOf({ valid: true, trusted: null }).kind).toBe("validUntrusted");
    expect(signatureBadgeOf({ valid: false, trusted: false, error: "bad" }).kind).toBe(
      "invalid",
    );
    expect(signatureBadgeOf(null).kind).toBe("unknown");
  });

  it("maps outbound pec states", () => {
    expect(pecOutboundStatusOf("delivered").color).toBe("success");
    expect(pecOutboundLabelKey("partiallyDelivered")).toBe(
      "pec.outbound_partiallyDelivered",
    );
    expect(pecDeliveryColor("warning")).toBe("warning");
    expect(pecDeliveryLabelKey("error")).toBe("pec.delivery_error");
  });

  it("applies presets without mutating silently beyond the target", () => {
    const form = {
      smtpPolicy: "standard",
      syncPaths: "*",
      idlePaths: "INBOX, Sent",
      pecEnabled: false,
    };
    applyPecEnabled(form);
    expect(form.pecEnabled).toBe(true);
    applyPecInboxOnly(form, { sendPolicy: "atMostOnce", switches: {}, syncPaths: ["INBOX"] });
    expect(form.syncPaths).toBe("INBOX");
    expect(form.idlePaths).toBe("INBOX");
    applyPecSendPolicy(form, {
      sendPolicy: "atMostOnce",
      switches: { "pec.enabled": true },
      syncPaths: null,
    });
    expect(form.smtpPolicy).toBe("atMostOnce");
    expect(joinPaths(null)).toBe("INBOX");
    expect(joinPaths(["A", "B"])).toBe("A, B");
  });
});
