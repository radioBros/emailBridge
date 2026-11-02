import { beforeEach, describe, expect, it, vi } from "vitest";
import { useEnrolmentFlow } from "@/features/auth/composables/useEnrolmentFlow";

vi.mock("@/api/authFlow", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/authFlow")>();
  return {
    ...actual,
    initiateTotpSetup: vi.fn(),
    finalizeTotpSetup: vi.fn(),
  };
});

vi.mock("@/api/auth", () => ({
  auth: {
    passkey: {
      register: vi.fn(),
    },
  },
}));

import { finalizeTotpSetup, initiateTotpSetup } from "@/api/authFlow";
import { auth } from "@/api/auth";

describe("useEnrolmentFlow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("walks TOTP setup through backup codes", async () => {
    vi.mocked(initiateTotpSetup).mockResolvedValue({
      uri: "otpauth://totp/EmailBridge:a@b.c?secret=ABCDEF",
      secret: "ABCDEF",
    });
    vi.mocked(finalizeTotpSetup).mockResolvedValue(["aaaa-bbbb", "cccc-dddd"]);
    const flow = useEnrolmentFlow();
    flow.chooseTotp();
    expect(flow.phase.value).toBe("totp");
    flow.password.value = "correct-horse-battery-staple";
    expect(await flow.startTotp()).toBe(true);
    expect(flow.setup.value?.secret).toBe("ABCDEF");
    flow.totpCode.value = "123456";
    expect(await flow.confirmTotp()).toBe(true);
    expect(flow.phase.value).toBe("backup_codes");
    expect(flow.backupCodes.value).toHaveLength(2);
    flow.finishBackupCodes();
    expect(flow.phase.value).toBe("done");
  });

  it("registers a passkey", async () => {
    vi.mocked(auth.passkey.register).mockResolvedValue({} as never);
    const flow = useEnrolmentFlow();
    flow.choosePasskey();
    flow.passkeyName.value = "Laptop";
    expect(await flow.registerPasskey()).toBe(true);
    expect(flow.phase.value).toBe("done");
    expect(auth.passkey.register).toHaveBeenCalledWith({ name: "Laptop" });
  });
});
