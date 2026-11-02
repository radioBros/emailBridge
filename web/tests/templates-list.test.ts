import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTemplates, sortParam } from "@/features/templates/composables/useTemplates";
import {
  emptyForm,
  formFromView,
  toCreateBody,
  toUpdateBody,
} from "@/features/templates/composables/useTemplateEditor";

vi.mock("@/api/templates", () => ({
  listTemplates: vi.fn(),
  createTemplate: vi.fn(),
  patchTemplate: vi.fn(),
  deleteTemplate: vi.fn(),
  getTemplate: vi.fn(),
  previewTemplate: vi.fn(),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import {
  createTemplate,
  deleteTemplate,
  listTemplates,
  patchTemplate,
} from "@/api/templates";

const sample = {
  id: "tpl_1",
  organizationId: "org_1",
  name: "Welcome",
  engine: "handlebars",
  subject: "Hi {{name}}",
  text: "Hello {{name}}",
  html: "<p>{{name}}</p>",
  currentVersion: 2,
  createdAt: "2026-10-01T00:00:00Z",
  updatedAt: "2026-10-25T00:00:00Z",
  version: 3,
};

describe("useTemplates", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sortParam defaults and maps", () => {
    expect(sortParam([])).toBe("-createdAt");
    expect(sortParam([{ key: "name", order: "asc" }])).toBe("name");
    expect(sortParam([{ key: "name", order: "desc" }])).toBe("-name");
  });

  it("loads and mutates", async () => {
    vi.mocked(listTemplates).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(createTemplate).mockResolvedValue(sample);
    vi.mocked(patchTemplate).mockResolvedValue({ ...sample, name: "Hi" });
    vi.mocked(deleteTemplate).mockResolvedValue(undefined);

    const w = useTemplates();
    await w.load();
    expect(w.rows.value).toHaveLength(1);
    expect(w.total.value).toBe(1);

    await w.setSearch("wel");
    expect(listTemplates).toHaveBeenCalledWith(expect.objectContaining({ q: "wel", page: 1 }));

    const created = await w.create({ name: "Welcome", subject: "Hi" });
    expect(created?.id).toBe("tpl_1");

    const updated = await w.update("tpl_1", { name: "Hi" });
    expect(updated?.name).toBe("Hi");

    expect(await w.remove("tpl_1")).toBe(true);
  });

  it("records load errors", async () => {
    vi.mocked(listTemplates).mockRejectedValue(new Error("boom"));
    const w = useTemplates();
    await w.load();
    expect(w.loadError.value).toBe("boom");
  });
});

describe("template editor helpers", () => {
  it("maps form <-> view", () => {
    const form = formFromView({
      ...sample,
      partials: { footer: "bye" },
      strict: true,
      locale: "it",
      timezone: "Europe/Rome",
    });
    expect(form.partials).toEqual([{ name: "footer", source: "bye" }]);
    expect(toCreateBody(form).partials).toEqual({ footer: "bye" });
    expect(toUpdateBody(form).strict).toBe(true);
    expect(emptyForm().name).toBe("");
  });
});
