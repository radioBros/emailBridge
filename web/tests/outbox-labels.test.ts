import { describe, expect, it } from "vitest";
import {
  attemptKindLabelKey,
  attemptStageLabelKey,
  copyToSentLabelKey,
} from "@/features/outbox/helpers/labels";
import { accountLabelOf, rememberAccounts } from "@/features/outbox/helpers/accountCache";

describe("outbox labels", () => {
  it("maps copy-to-sent and attempt keys", () => {
    expect(copyToSentLabelKey("pending")).toBe("outbox.copy_state_pending");
    expect(copyToSentLabelKey("weird")).toBe("");
    expect(attemptStageLabelKey("data")).toBe("outbox.stage_data");
    expect(attemptKindLabelKey("outcome_unknown")).toBe("outbox.kind_outcome_unknown");
  });

  it("caches account labels", () => {
    rememberAccounts([
      {
        id: "acc_01",
        name: "Support",
        email: "support@example.test",
        organizationId: "org_1",
        state: "connected",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        version: 1,
        imap: {} as never,
        smtp: {} as never,
        sync: {},
      },
    ]);
    expect(accountLabelOf("acc_01").name).toBe("Support");
    expect(accountLabelOf("acc_missing").name).toBe("acc_missing");
  });
});
