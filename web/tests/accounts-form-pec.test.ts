import { describe, expect, it } from "vitest";
import { emptyForm, toCreateInput, toPatchBody } from "@/features/accounts/helpers/accountForm";

describe("account form: pec.enabled (S17 §10)", () => {
  it("sends the switch only when it is on", () => {
    const f = { ...emptyForm(), name: "PEC", email: "a@pec.it", imapHost: "imap.pec.it", smtpHost: "smtp.pec.it" };
    expect(toCreateInput(f).switches).toBeUndefined();
    expect(toPatchBody(f).switches).toEqual({ "accounts.ignoreCertErrors": false });

    f.pecEnabled = true;
    expect(toCreateInput(f).switches).toEqual({ "pec.enabled": true });
    expect(toPatchBody(f).switches).toEqual({ "accounts.ignoreCertErrors": false, "pec.enabled": true });

    f.ignoreCertErrors = true;
    expect(toCreateInput(f).switches).toEqual({ "accounts.ignoreCertErrors": true, "pec.enabled": true });
  });
});
