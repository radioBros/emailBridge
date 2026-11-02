import { beforeEach, describe, expect, it, vi } from "vitest";
import { useTemplateEditor } from "@/features/templates/composables/useTemplateEditor";

vi.mock("@/api/templates", () => ({
  getTemplate: vi.fn(),
  createTemplate: vi.fn(),
  patchTemplate: vi.fn(),
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
  getTemplate,
  patchTemplate,
  previewTemplate,
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

describe("useTemplateEditor", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads, saves, and previews", async () => {
    vi.mocked(getTemplate).mockResolvedValue(sample);
    vi.mocked(patchTemplate).mockResolvedValue({ ...sample, currentVersion: 3, version: 4 });
    vi.mocked(createTemplate).mockResolvedValue(sample);
    vi.mocked(previewTemplate).mockResolvedValue({ subject: "Hi Ada", text: "Hello Ada" });

    const e = useTemplateEditor();
    await e.load("tpl_1");
    expect(e.form.value.name).toBe("Welcome");
    expect(e.variableHints.value).toContain("name");
    expect(e.versionOptions.value).toEqual([2, 1]);

    e.sampleVars.value = '{"name":"Ada"}';
    const preview = await e.runPreview();
    expect(preview?.subject).toBe("Hi Ada");

    const saved = await e.save();
    expect(saved?.currentVersion).toBe(3);

    e.resetNew();
    e.form.value.name = "New";
    e.form.value.subject = "Subj";
    const created = await e.save();
    expect(created?.id).toBe("tpl_1");
  });

  it("rejects bad sample JSON", async () => {
    vi.mocked(getTemplate).mockResolvedValue(sample);
    const e = useTemplateEditor();
    await e.load("tpl_1");
    e.sampleVars.value = "{";
    expect(await e.runPreview()).toBeNull();
    expect(e.previewError.value).toBe("json");
  });
});
