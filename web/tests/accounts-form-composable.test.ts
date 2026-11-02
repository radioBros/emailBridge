import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

vi.mock("@/api/accounts", () => ({
  getAccount: vi.fn(),
  createAccount: vi.fn(async () => ({ id: "acc_new" })),
  patchAccount: vi.fn(),
  verifyAccountSettings: vi.fn(async () => ({
    imap: { ok: true, latencyMs: 10 },
    smtp: { ok: true, latencyMs: 12 },
  })),
}));

import { createAccount, verifyAccountSettings } from "@/api/accounts";
import { useAccountForm } from "@/features/accounts/composables/useAccountForm";

describe("useAccountForm (create)", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("initialises empty form and walks steps", async () => {
    const { load, form, step, goNext, goPrev, runVerify, save, isEdit } = useAccountForm();
    await load();
    expect(isEdit.value).toBe(false);
    expect(form.value.name).toBe("");
    goNext();
    expect(step.value).toBe(2);
    goPrev();
    expect(step.value).toBe(1);

    form.value.name = "N";
    form.value.email = "a@b.co";
    form.value.imapHost = "imap.test";
    form.value.imapPort = 993;
    form.value.imapUser = "u";
    form.value.imapPass = "p";
    form.value.smtpHost = "smtp.test";
    form.value.smtpPort = 465;
    form.value.smtpUser = "u";
    form.value.smtpPass = "p";

    const ok = await runVerify();
    expect(ok).toBe(true);
    expect(verifyAccountSettings).toHaveBeenCalled();

    await save();
    expect(createAccount).toHaveBeenCalledWith(
      expect.not.objectContaining({ skipVerify: true }),
    );
  });

  it("sends skipVerify only when asked after a failed verify", async () => {
    vi.mocked(verifyAccountSettings).mockResolvedValueOnce({
      imap: { ok: true, latencyMs: 10 },
      smtp: { ok: false, code: "smtp_auth_failed", detail: "535 Authentication failed" },
    });
    const { load, form, runVerify, save, verifyPassed } = useAccountForm();
    await load();
    form.value.name = "N";
    form.value.email = "a@b.co";
    form.value.imapHost = "imap.test";
    form.value.imapPort = 993;
    form.value.imapUser = "u";
    form.value.imapPass = "p";
    form.value.smtpHost = "smtp.test";
    form.value.smtpPort = 465;
    form.value.smtpUser = "u";
    form.value.smtpPass = "p";

    expect(await runVerify()).toBe(false);
    expect(verifyPassed.value).toBe(false);

    await save({ skipVerify: true });
    expect(createAccount).toHaveBeenCalledWith(
      expect.objectContaining({ skipVerify: true }),
    );
  });
});
