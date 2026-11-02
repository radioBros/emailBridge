import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import { createVuetify } from "vuetify";
import { aliases, mdiSet } from "@/core/config/MdiIconSet";
import { dark, light } from "@/core/config/VuetifyThemes";
import defaults, { componentAliases } from "@/core/config/VuetifyDefaults";
import MessageReader from "@/features/messages/components/MessageReader.vue";
import en from "@/features/messages/locales/en.json";

vi.mock("vue-i18n", async (orig) => {
  const actual = await orig<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => (err instanceof Error ? err.message : String(err)),
    fieldErrors: () => ({}),
  }),
}));

const getMessage = vi.fn();
const getMessageRaw = vi.fn();
const patchMessage = vi.fn();
const listMessages = vi.fn();
const deleteMessage = vi.fn();
const downloadMessageRaw = vi.fn();
const downloadAttachment = vi.fn();

vi.mock("@/api/messages", () => ({
  getMessage: (...a: unknown[]) => getMessage(...a),
  getMessageRaw: (...a: unknown[]) => getMessageRaw(...a),
  patchMessage: (...a: unknown[]) => patchMessage(...a),
  listMessages: (...a: unknown[]) => listMessages(...a),
  deleteMessage: (...a: unknown[]) => deleteMessage(...a),
  moveMessage: vi.fn(),
  downloadMessageRaw: (...a: unknown[]) => downloadMessageRaw(...a),
  downloadAttachment: (...a: unknown[]) => downloadAttachment(...a),
}));

import { useMessageReader } from "@/features/messages/composables/useMessageReader";
import { FLAG_SEEN } from "@/features/messages/helpers/flags";

const rawText = [
  "From: a@test",
  "To: b@test",
  "Subject: Hi",
  "Content-Type: text/html",
  "",
  "<p>Hello</p>",
].join("\r\n");

const baseMsg = {
  id: "msg_1",
  accountId: "acc_1",
  mailboxId: "mbx_1",
  uidValidity: 1,
  uid: 1,
  flags: [] as string[],
  emailId: "e1",
  threadId: "th_1",
  internalDate: "2026-10-26T10:00:00Z",
  size: 100,
  subject: "Hi",
  from: [{ address: "a@test" }],
  to: [{ address: "b@test" }],
  text: "Hello plain",
  headers: { Subject: "Hi" },
};

describe("useMessageReader", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    getMessage.mockResolvedValue({
      ...baseMsg,
      webSafeHtml: "<p>Safe</p>",
      html: "<p onclick=x>Unsafe</p>",
      attachments: [
        {
          id: "att_01hxyzabcdefghijklmnopqrstuv",
          filename: "ok.pdf",
          contentType: "application/pdf",
          size: 10,
          scan: "clean",
        },
        {
          id: "att_01hxyzabcdefghijklmnopqrstuw",
          filename: "bad.exe",
          contentType: "application/octet-stream",
          size: 20,
          scan: "infected",
        },
      ],
    });
    getMessageRaw.mockResolvedValue({
      ok: true,
      bytes: new TextEncoder().encode(rawText).buffer,
      contentType: "message/rfc822",
    });
    listMessages.mockResolvedValue({
      data: [
        {
          id: "msg_2",
          accountId: "acc_1",
          mailboxId: "mbx_other",
          uidValidity: 1,
          uid: 2,
          flags: [FLAG_SEEN],
          emailId: "e2",
          threadId: "th_1",
          internalDate: "2026-10-26T11:00:00Z",
          size: 80,
        },
      ],
    });
    patchMessage.mockImplementation(async (_a, _id, body: { addFlags?: string[] }) => ({
      ...baseMsg,
      flags: body.addFlags ?? [FLAG_SEEN],
    }));
  });

  it("loads detail, prefers webSafeHtml, and fetches thread by threadId", async () => {
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    expect(getMessage).toHaveBeenCalledWith(
      "acc_1",
      "msg_1",
      expect.objectContaining({ text: "both", headers: true, envelope: true }),
    );
    expect(r.detail.value?.subject).toBe("Hi");
    expect(r.hasHtml.value).toBe(true);
    expect(r.htmlBody.value).toBe("<p>Safe</p>");
    expect(r.htmlBody.value).not.toContain("onclick");
    expect(r.unread.value).toBe(true);
    expect(listMessages).toHaveBeenCalledWith(
      "acc_1",
      expect.objectContaining({ threadId: "th_1" }),
    );
    expect(listMessages.mock.calls[0][1]).not.toHaveProperty("mailboxId");
    expect(r.threadPeers.value).toHaveLength(1);
    expect(r.threadPeers.value[0].mailboxId).toBe("mbx_other");
  });

  it("falls back to text when only raw html is present (old servers)", async () => {
    getMessage.mockResolvedValue({
      ...baseMsg,
      html: "<script>alert(1)</script><p>Raw</p>",
    });
    getMessageRaw.mockResolvedValue({ ok: false, missing: true });
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    expect(r.hasHtml.value).toBe(false);
    expect(r.htmlBody.value).toBe("");
    expect(r.hasText.value).toBe(true);
    expect(r.textBody.value).toBe("Hello plain");
  });

  it("marks seen and deletes", async () => {
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    await r.toggleSeen();
    expect(patchMessage).toHaveBeenCalled();
    deleteMessage.mockResolvedValue(undefined);
    const ok = await r.remove();
    expect(ok).toBe(true);
    expect(r.detail.value).toBeNull();
  });

  it("handles missing raw", async () => {
    getMessageRaw.mockResolvedValue({ ok: false, missing: true });
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    expect(r.rawMissing.value).toBe(true);
  });

  it("toggles flagged and downloads raw", async () => {
    downloadMessageRaw.mockResolvedValue({
      ok: true,
      bytes: new ArrayBuffer(1),
      contentType: "message/rfc822",
    });
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    await r.toggleFlagged();
    expect(patchMessage).toHaveBeenCalled();
    await r.downloadRaw();
    expect(downloadMessageRaw).toHaveBeenCalled();
  });

  it("uses att_ ids and blocks infected downloads", async () => {
    downloadAttachment.mockResolvedValue({
      ok: true,
      bytes: new ArrayBuffer(1),
      contentType: "application/pdf",
    });
    const r = useMessageReader();
    await r.show("acc_1", "mbx_1", "msg_1");
    expect(r.attachments.value).toHaveLength(2);
    const clean = r.attachments.value[0];
    const infected = r.attachments.value[1];
    await r.downloadPart(clean);
    expect(downloadAttachment).toHaveBeenCalledWith(
      "acc_1",
      "msg_1",
      "att_01hxyzabcdefghijklmnopqrstuv",
      "ok.pdf",
    );
    await r.downloadPart(infected);
    expect(r.attBlocked.value).toBe(infected.id);
    expect(downloadAttachment).toHaveBeenCalledTimes(1);
  });
});

describe("MessageReader component", () => {
  const i18n = createI18n({
    legacy: false,
    locale: "en",
    messages: { en },
  });
  const vuetify = createVuetify({
    theme: { defaultTheme: "light", themes: { light, dark } },
    defaults,
    aliases: componentAliases,
    icons: { defaultSet: "mdi", aliases, sets: { mdi: mdiSet } },
  });

  const baseProps = {
    loadError: null as string | null,
    loadImages: false,
    rawMissing: false,
    textBody: "Hello plain",
    hasText: true,
    attachments: [],
    threadPeers: [],
    unread: false,
    flagged: false,
    busy: false,
    downloading: false,
    isFirstLoad: false,
    isRefreshing: false,
  };

  it("renders sandboxed webSafeHtml and never raw html", () => {
    const w = mount(MessageReader, {
      props: {
        ...baseProps,
        detail: {
          ...baseMsg,
          webSafeHtml: "<p>Safe body</p>",
          html: "<script>evil</script>",
        },
        htmlBody: "<p>Safe body</p>",
        hasHtml: true,
      },
      global: { plugins: [vuetify, i18n] },
    });
    const iframe = w.find("iframe");
    expect(iframe.exists()).toBe(true);
    expect(iframe.attributes("srcdoc") ?? "").toContain("Safe body");
    expect(iframe.attributes("srcdoc") ?? "").not.toContain("evil");
    expect(iframe.attributes("sandbox")).toBe("");
  });

  it("hides html tab when only raw html is available", () => {
    const w = mount(MessageReader, {
      props: {
        ...baseProps,
        detail: { ...baseMsg, html: "<p>Raw only</p>" },
        htmlBody: "",
        hasHtml: false,
      },
      global: { plugins: [vuetify, i18n] },
    });
    expect(w.text()).toContain("Hello plain");
    expect(w.find("iframe").exists()).toBe(false);
  });
});
