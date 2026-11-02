import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("@/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
  return {
    ...actual,
    api: {
      post: vi.fn(),
    },
  };
});

import { api } from "@/api/client";
import { submitOutbound } from "@/api/outboxSubmit";

describe("outbox submit api", () => {
  beforeEach(() => {
    vi.mocked(api.post).mockReset();
  });

  it("posts tracking overrides and listId", async () => {
    vi.mocked(api.post).mockResolvedValue({
      id: "out_1",
      messageId: "<x@y>",
      state: "queued",
    } as never);
    const res = await submitOutbound("acc_1", {
      to: [{ address: "a@b.test" }],
      subject: "Hi",
      html: "<p>x</p>",
      trackOpens: false,
      trackClicks: true,
      listId: "news.example.com",
    });
    expect(res.id).toBe("out_1");
    expect(api.post).toHaveBeenCalledWith(
      "/accounts/acc_1/outbound-messages",
      expect.objectContaining({
        trackOpens: false,
        trackClicks: true,
        listId: "news.example.com",
      }),
    );
  });
});
