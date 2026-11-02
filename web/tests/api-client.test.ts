import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fetchMock = vi.fn();

describe("api client", () => {
  beforeEach(() => {
    vi.resetModules();
    fetchMock.mockReset();
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("window", {
      location: { pathname: "/accounts", assign: vi.fn(), origin: "http://localhost" },
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("sends CSRF header and parses problems", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          status: 422,
          code: "validation_failed",
          title: "Validation failed",
          detail: "bad",
        }),
        { status: 422, headers: { "Content-Type": "application/json" } },
      ),
    );

    const { api, ApiProblemError, configureApiClient } = await import("@/api/client");
    configureApiClient({ getOrganizationId: () => "org-1" });

    await expect(api.get("/me")).rejects.toBeInstanceOf(ApiProblemError);

    const init = (fetchMock.mock.calls[0][1] ?? {}) as RequestInit;
    const req = fetchMock.mock.calls[0][0];
    const headers = new Headers(req instanceof Request ? req.headers : init.headers);
    expect(headers.get("X-Requested-With")).toBe("EmailBridge");
    expect(headers.get("EB-Organization")).toBe("org-1");
  });

  it("redirects on 401 unauthenticated", async () => {
    const assign = vi.fn();
    vi.stubGlobal("window", {
      location: { pathname: "/accounts", assign, origin: "http://localhost" },
    });

    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({ status: 401, code: "unauthenticated", title: "Unauthenticated" }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      ),
    );

    const { api, configureApiClient } = await import("@/api/client");
    configureApiClient({
      getOrganizationId: () => null,
      onUnauthorized: () => assign("/login"),
    });

    await expect(api.get("/me")).rejects.toThrow();
    expect(assign).toHaveBeenCalledWith("/login");
  });

  it("retries idempotent GET once on network error", async () => {
    fetchMock
      .mockRejectedValueOnce(new TypeError("network"))
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ ok: true }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );

    const { api, configureApiClient } = await import("@/api/client");
    configureApiClient({ getOrganizationId: () => null });

    const data = await api.get<{ ok: boolean }>("/health");
    expect(data.ok).toBe(true);
    expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(2);
  });
});
