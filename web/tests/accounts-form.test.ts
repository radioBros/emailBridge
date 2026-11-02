import { describe, expect, it } from "vitest";
import {
  emptyForm,
  formFromAccount,
  hostRules,
  portRules,
  toCreateInput,
  toPatchBody,
  toVerifyRequest,
} from "@/features/accounts/helpers/accountForm";
import type { AccountView } from "@/api/accounts";

const sampleAccount = (): AccountView =>
  ({
    id: "acc_1",
    organizationId: "org_1",
    name: "Inbox",
    email: "mail@example.test",
    state: "connected",
    externalId: "ext-1",
    imap: {
      host: "imap.example.test",
      port: 993,
      tls: "implicit",
      auth: { user: "mail@example.test", hasPassword: true },
    },
    smtp: {
      host: "smtp.example.test",
      port: 465,
      tls: "implicit",
      policy: "standard",
      auth: { user: "mail@example.test", hasPassword: true },
    },
    sync: { paths: ["*"], idlePaths: ["INBOX"] },
    switches: { "accounts.ignoreCertErrors": false },
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    version: 3,
  }) as AccountView;

describe("accountForm helpers", () => {
  it("validates host and port", () => {
    expect(hostRules[0]!("mail.example.com")).toBe(true);
    expect(hostRules[0]!("bad host")).toBe("invalid_host");
    expect(hostRules[0]!("")).toBe(true);
    expect(portRules[0]!(993)).toBe(true);
    expect(portRules[0]!(0)).toBe("invalid_port");
    expect(portRules[0]!(70000)).toBe("invalid_port");
    expect(portRules[0]!("")).toBe(true);
  });

  it("defaults timezone and locale on empty form", () => {
    const f = emptyForm();
    expect(f.tz.length).toBeGreaterThan(0);
    expect(f.locale).toBe("it");
  });

  it("keeps supported account locales from the API and falls back otherwise", () => {
    const withJa = formFromAccount({ ...sampleAccount(), locale: "ja" } as AccountView);
    expect(withJa.locale).toBe("ja");
    const withFr = formFromAccount({ ...sampleAccount(), locale: "fr" } as AccountView);
    expect(withFr.locale).toBe("fr");
    const unknown = formFromAccount({ ...sampleAccount(), locale: "xx" } as AccountView);
    expect(unknown.locale).toBe("it");
    const empty = formFromAccount({ ...sampleAccount(), locale: "" } as AccountView);
    expect(empty.locale).toBe("it");
  });

  it("builds create payload with passwords and paths", () => {
    const f = emptyForm();
    f.name = "Test";
    f.email = "a@b.co";
    f.imapHost = "imap.example.test";
    f.imapPort = 993;
    f.imapUser = "a@b.co";
    f.imapPass = "secret";
    f.smtpHost = "smtp.example.test";
    f.smtpPort = 465;
    f.smtpUser = "a@b.co";
    f.smtpPass = "secret";
    f.smtpPolicy = "atMostOnce";
    f.syncPaths = "INBOX, Sent";
    f.idlePaths = "INBOX";
    f.ignoreCertErrors = true;

    const body = toCreateInput(f);
    expect(body.name).toBe("Test");
    expect(body.imap.auth.pass).toBe("secret");
    expect(body.smtp.policy).toBe("atMostOnce");
    expect(body.sync?.paths).toEqual(["INBOX", "Sent"]);
    expect(body.switches?.["accounts.ignoreCertErrors"]).toBe(true);
  });

  it("omits unchanged passwords on patch", () => {
    const f = formFromAccount(sampleAccount());
    expect(f.imapPass).toBe("");
    expect(f.smtpPass).toBe("");
    const patch = toPatchBody(f);
    expect(patch.imap?.auth.pass).toBeUndefined();
    expect(patch.smtp?.auth.pass).toBeUndefined();
    expect(patch.name).toBe("Inbox");

    f.imapPass = "new-secret";
    const withPass = toPatchBody(f);
    expect(withPass.imap?.auth.pass).toBe("new-secret");
  });

  it("builds verify request honouring edit password rules", () => {
    const f = formFromAccount(sampleAccount());
    const editReq = toVerifyRequest(f, true);
    expect(editReq.imap?.auth.pass).toBeUndefined();
    f.smtpPass = "x";
    const withSmtp = toVerifyRequest(f, true);
    expect(withSmtp.smtp?.auth.pass).toBe("x");
    expect(withSmtp.ignoreCertErrors).toBe(false);
  });

  it("serialises network and IMAP ID overlay fields (W57)", () => {
    const f = emptyForm();
    f.name = "N";
    f.email = "a@b.co";
    f.imapHost = "imap.example.test";
    f.imapPort = 993;
    f.imapUser = "a";
    f.imapPass = "p";
    f.smtpHost = "smtp.example.test";
    f.smtpPort = 465;
    f.smtpUser = "a";
    f.smtpPass = "p";
    f.imapIdName = "AcmeMail";
    f.imapIdVersion = "2.0";
    f.imapDisableRev2 = true;
    f.netProxy = "socks5://127.0.0.1:1080";
    f.netLocalAddress = "127.0.0.1";
    f.netLocalAddresses = "127.0.0.1, 127.0.0.2";
    f.netImapStrategy = "hash-by-account";
    const body = toCreateInput(f) as ReturnType<typeof toCreateInput> & {
      network?: { proxy?: string; localAddresses?: string[]; imapStrategy?: string };
      imap: { idName?: string; idVersion?: string; disableRev2?: boolean };
    };
    expect(body.imap.idName).toBe("AcmeMail");
    expect(body.imap.idVersion).toBe("2.0");
    expect(body.imap.disableRev2).toBe(true);
    expect(body.network?.proxy).toBe("socks5://127.0.0.1:1080");
    expect(body.network?.localAddresses).toEqual(["127.0.0.1", "127.0.0.2"]);
    expect(body.network?.imapStrategy).toBe("hash-by-account");

    const from = formFromAccount({
      ...sampleAccount(),
      imap: {
        ...sampleAccount().imap,
        idName: "X",
        idVersion: "1",
        disableRev2: true,
      } as AccountView["imap"],
      network: {
        proxy: "http://proxy:8080",
        localAddress: "10.0.0.1",
        localAddresses: ["10.0.0.2"],
        smtpStrategy: "random",
      },
    } as AccountView);
    expect(from.netProxy).toBe("http://proxy:8080");
    expect(from.netLocalAddresses).toBe("10.0.0.2");
    expect(from.imapDisableRev2).toBe(true);
    const patch = toPatchBody(from) as ReturnType<typeof toPatchBody> & {
      network?: { proxy?: string } | null;
    };
    expect(patch.network?.proxy).toBe("http://proxy:8080");
  });
});
