/* eslint-disable vue/one-component-per-file -- inline stubs for the lazy editors and a setup host */
import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import en from "@/features/templates/locales/en.json";
import { ApiProblemError } from "@/api/client";

vi.mock("@/api/templates", () => ({
  getTemplate: vi.fn(),
  createTemplate: vi.fn(),
  patchTemplate: vi.fn(),
  previewTemplate: vi.fn(),
}));

const route = { name: "templates-new", params: {} as Record<string, string> };
const router = { push: vi.fn(), replace: vi.fn() };
vi.mock("vue-router", () => ({ useRoute: () => route, useRouter: () => router }));

// The real editors are lazy chunks (TipTap, CodeMirror); the page logic is what is under test here.
vi.mock("@/features/outbox/components/RichTextEditor.vue", () => ({
  __esModule: true,
  default: defineComponent({
    props: { modelValue: { type: String, default: "" } },
    emits: ["update:modelValue"],
    setup: (props) => () => h("div", { "data-testid": "rich-text-stub" }, props.modelValue),
  }),
}));
vi.mock("@/features/templates/components/MjmlEditor.vue", () => ({
  __esModule: true,
  default: defineComponent({
    props: {
      modelValue: { type: String, default: "" },
      errors: { type: Array, default: () => [] },
    },
    emits: ["update:modelValue"],
    setup: (props, { expose }) => {
      expose({ reveal: vi.fn() });
      return () =>
        h("textarea", {
          "data-testid": "mjml-stub",
          "data-errors": String(props.errors.length),
          value: props.modelValue,
        });
    },
  }),
}));

import { createTemplate, getTemplate, patchTemplate } from "@/api/templates";
import { useTemplateEditor, toCreateBody, toUpdateBody } from "@/features/templates/composables/useTemplateEditor";
import {
  MJML_STARTER,
  formatOf,
  isBlankSource,
  mjmlErrorsOf,
} from "@/features/templates/helpers/mjml";
import TemplateEditorPage from "@/features/templates/pages/TemplateEditorPage.vue";

const MJML = "<mjml><mj-body><mj-section><mj-column><mj-text>Hi {{name}}</mj-text></mj-column></mj-section></mj-body></mjml>";

const mjmlView = {
  id: "tpl_1",
  organizationId: "org_1",
  name: "Responsive",
  engine: "handlebars",
  html: MJML,
  htmlFormat: "mjml" as const,
  htmlCompiled: "<!doctype html><html><body><div>Hi {{name}}</div></body></html>",
  currentVersion: 1,
  createdAt: "2026-10-30T00:00:00Z",
  updatedAt: "2026-10-30T00:00:00Z",
  version: 1,
};

const compileProblem = () =>
  new ApiProblemError({
    status: 422,
    code: "validation_failed",
    detail: "Invalid MJML in html: Line 3, mj-bogus: Element mj-bogus doesn't exist or is not registered",
    errors: [
      { path: "body.html", code: "invalid", message: "Line 3, mj-bogus: Element mj-bogus doesn't exist or is not registered" },
      { path: "body.html", code: "timeout", message: "MJML compilation did not finish in time." },
      { path: "body.name", code: "invalid", message: "unrelated" },
    ],
  });

const i18n = () =>
  createI18n({
    legacy: false,
    locale: "en",
    missingWarn: false,
    fallbackWarn: false,
    messages: {
      en: {
        ...en,
        common: { search: "Search", close: "Close", states: { loading: "Loading", refreshing: "Refreshing" } },
      },
    },
  });

/** Runs the composable inside a component setup (it reads i18n through useProblem). */
const withEditor = (): ReturnType<typeof useTemplateEditor> => {
  let api: ReturnType<typeof useTemplateEditor> | null = null;
  mount(
    defineComponent({
      setup: () => {
        api = useTemplateEditor();
        return () => h("div");
      },
    }),
    { global: { plugins: [i18n()] } },
  );
  if (!api) throw new Error("composable not set up");
  return api;
};

describe("MJML helpers", () => {
  it("reads the server's compile errors with their lines", () => {
    expect(mjmlErrorsOf(compileProblem().problem)).toEqual([
      { message: "Line 3, mj-bogus: Element mj-bogus doesn't exist or is not registered", line: 3 },
      { message: "MJML compilation did not finish in time." },
    ]);
    expect(mjmlErrorsOf(null)).toEqual([]);
  });

  it("treats templates without a format as HTML", () => {
    expect(formatOf({})).toBe("html");
    expect(formatOf({ htmlFormat: "mjml" })).toBe("mjml");
  });

  it("detects blank rich-text sources", () => {
    expect(isBlankSource("")).toBe(true);
    expect(isBlankSource("<p></p>")).toBe(true);
    expect(isBlankSource("<p><br></p> &nbsp;")).toBe(true);
    expect(isBlankSource("<p>x</p>")).toBe(false);
  });
});

describe("useTemplateEditor format toggle", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("starts an empty part from the starter and restores each format's source", () => {
    const e = withEditor();
    e.resetNew();
    e.setHtmlFormat("mjml");
    expect(e.form.value.htmlFormat).toBe("mjml");
    expect(e.form.value.html).toBe(MJML_STARTER);
    e.form.value.html = MJML;
    e.setHtmlFormat("html");
    expect(e.form.value.html).toBe("");
    e.form.value.html = "<p>rich</p>";
    e.setHtmlFormat("mjml");
    expect(e.form.value.html).toBe(MJML);
    e.setHtmlFormat("html");
    expect(e.form.value.html).toBe("<p>rich</p>");
  });

  it("sends htmlFormat and reads compiled HTML", async () => {
    vi.mocked(getTemplate).mockResolvedValue(mjmlView);
    const e = withEditor();
    await e.load("tpl_1");
    expect(e.form.value.htmlFormat).toBe("mjml");
    expect(e.compiledHtml.value).toContain("Hi {{name}}");
    expect(toUpdateBody(e.form.value).htmlFormat).toBe("mjml");
    expect(toCreateBody({ ...e.form.value, htmlFormat: "html" }).htmlFormat).toBeUndefined();
    expect(e.variableHints.value).toContain("name");
  });

  it("lists compile errors at the editor instead of the page alert", async () => {
    vi.mocked(getTemplate).mockResolvedValue(mjmlView);
    vi.mocked(patchTemplate).mockRejectedValue(compileProblem());
    const e = withEditor();
    await e.load("tpl_1");
    expect(await e.save()).toBeNull();
    expect(e.htmlErrors.value).toHaveLength(2);
    expect(e.formError.value).toBeNull();
    e.setHtmlFormat("html");
    expect(e.htmlErrors.value).toEqual([]);

    vi.mocked(patchTemplate).mockRejectedValue(new Error("boom"));
    await e.save();
    expect(e.formError.value).toBeTruthy();
    expect(e.htmlErrors.value).toEqual([]);
  });
});

const mountPage = () => mount(TemplateEditorPage, { global: { plugins: [i18n()], directives: { tippy: {} } } });

describe("TemplateEditorPage HTML / MJML toggle", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    route.name = "templates-new";
    route.params = {};
  });

  it("switches editors, saves MJML and shows the compile errors inline", async () => {
    vi.mocked(createTemplate).mockRejectedValueOnce(compileProblem());
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="rich-text-stub"]').exists()).toBe(true);
    expect(w.find('[data-testid="mjml-stub"]').exists()).toBe(false);

    await w.find('[data-testid="templates-format-mjml"]').trigger("click");
    await flushPromises();
    const editor = w.find('[data-testid="mjml-stub"]');
    expect(editor.exists()).toBe(true);
    expect((editor.element as HTMLTextAreaElement).value).toBe(MJML_STARTER);
    expect(w.find('[data-testid="rich-text-stub"]').exists()).toBe(false);
    expect(w.text()).toContain(en.templates.format_mjml_hint);

    await w.find('[data-testid="templates-name"] input').setValue("Responsive");
    await w.find('[data-testid="templates-save"]').trigger("click");
    await flushPromises();
    expect(createTemplate).toHaveBeenCalledWith(
      expect.objectContaining({ name: "Responsive", htmlFormat: "mjml", html: MJML_STARTER }),
    );
    const alert = w.find('[data-testid="templates-mjml-errors"]');
    expect(alert.exists()).toBe(true);
    expect(alert.text()).toContain("mj-bogus");
    expect(alert.text()).toContain("did not finish in time");
    expect(w.find('[data-testid="mjml-stub"]').attributes("data-errors")).toBe("2");
    expect(alert.findAll("button")).toHaveLength(1);

    await w.find('[data-testid="templates-format-html"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="rich-text-stub"]').exists()).toBe(true);
    expect(w.find('[data-testid="templates-mjml-errors"]').exists()).toBe(false);
    w.unmount();
  });

  it("shows the compiled HTML of a saved MJML template", async () => {
    route.name = "templates-edit";
    route.params = { templateId: "tpl_1" };
    vi.mocked(getTemplate).mockResolvedValue(mjmlView);
    const w = mountPage();
    await flushPromises();
    expect(w.find('[data-testid="mjml-stub"]').exists()).toBe(true);
    expect(w.find('[data-testid="templates-mjml-compiled"]').exists()).toBe(true);
    w.unmount();
  });
});
