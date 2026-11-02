import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import { api } from "@/api/client";
import {
  createTemplate,
  deleteTemplate,
  getTemplate,
  listTemplates,
  patchTemplate,
  previewTemplate,
} from "@/api/templates";

describe("templates api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists with query params", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [], nextCursor: null, total: 0 });
    await listTemplates({ page: 2, limit: 10, sort: "name", q: "welcome" });
    expect(api.get).toHaveBeenCalledWith(
      expect.stringMatching(/\/templates\?.*page=2.*limit=10/),
    );
    expect(api.get).toHaveBeenCalledWith(expect.stringContaining("q=welcome"));
  });

  it("CRUD + preview paths", async () => {
    vi.mocked(api.get).mockResolvedValue({ id: "tpl_1" });
    vi.mocked(api.post).mockResolvedValue({ id: "tpl_1" });
    vi.mocked(api.patch).mockResolvedValue({ id: "tpl_1" });
    vi.mocked(api.delete).mockResolvedValue(undefined);
    await getTemplate("tpl_1");
    await createTemplate({ name: "Welcome", subject: "Hi {{name}}" });
    await patchTemplate("tpl_1", { subject: "Hey" });
    await previewTemplate("tpl_1", { variables: { name: "Ada" } });
    await deleteTemplate("tpl_1");
    expect(api.get).toHaveBeenCalledWith("/templates/tpl_1");
    expect(api.post).toHaveBeenCalledWith("/templates/tpl_1/preview", {
      variables: { name: "Ada" },
    });
    expect(api.delete).toHaveBeenCalledWith("/templates/tpl_1");
  });
});
