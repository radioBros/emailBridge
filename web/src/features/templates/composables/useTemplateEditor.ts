import { computed, ref } from "vue";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import {
  createTemplate,
  getTemplate,
  patchTemplate,
  previewTemplate,
  type TemplateCreate,
  type TemplatePreviewResult,
  type TemplateUpdate,
  type TemplateView,
} from "@/api/templates";
import {
  DEFAULT_SAMPLE_VARS,
  extractHbsVariables,
  parseSampleVariables,
} from "@/features/templates/helpers/hbsHints";
import {
  MJML_STARTER,
  formatOf,
  isBlankSource,
  mjmlErrorsOf,
  type HtmlFormat,
  type MjmlError,
  type TemplateFormatFields,
} from "@/features/templates/helpers/mjml";

export type EditorForm = {
  name: string;
  subject: string;
  text: string;
  html: string;
  /** Format of `html`: HTML (rich-text editor) or MJML (code editor, compiled on the server). */
  htmlFormat: HtmlFormat;
  strict: boolean;
  locale: string;
  timezone: string;
  partials: { name: string; source: string }[];
};

export const emptyForm = (): EditorForm => ({
  name: "",
  subject: "",
  text: "",
  html: "",
  htmlFormat: "html",
  strict: false,
  locale: "en",
  timezone: "UTC",
  partials: [],
});

export const formFromView = (t: TemplateView & TemplateFormatFields): EditorForm => ({
  name: t.name,
  subject: t.subject ?? "",
  text: t.text ?? "",
  html: t.html ?? "",
  htmlFormat: formatOf(t),
  strict: Boolean(t.strict),
  locale: t.locale || "en",
  timezone: t.timezone || "UTC",
  partials: Object.entries(t.partials ?? {}).map(([name, source]) => ({ name, source })),
});

export const toCreateBody = (f: EditorForm): TemplateCreate & TemplateFormatFields => {
  const partials: Record<string, string> = {};
  for (const p of f.partials) {
    const n = p.name.trim();
    if (n) partials[n] = p.source;
  }
  return {
    name: f.name.trim(),
    subject: f.subject || undefined,
    text: f.text || undefined,
    html: f.html || undefined,
    htmlFormat: f.htmlFormat === "mjml" ? "mjml" : undefined,
    partials: Object.keys(partials).length ? partials : undefined,
    strict: f.strict || undefined,
    locale: f.locale.trim() || undefined,
    timezone: f.timezone.trim() || undefined,
  };
};

export const toUpdateBody = (f: EditorForm): TemplateUpdate & TemplateFormatFields => {
  const body = toCreateBody(f);
  return {
    name: body.name,
    subject: body.subject ?? "",
    text: body.text ?? "",
    html: body.html ?? "",
    htmlFormat: f.htmlFormat,
    partials: body.partials ?? {},
    strict: Boolean(f.strict),
    locale: body.locale ?? "",
    timezone: body.timezone ?? "",
  };
};

export const useTemplateEditor = () => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf, fieldErrors } = useProblem();

  const form = ref<EditorForm>(emptyForm());
  const current = ref<(TemplateView & TemplateFormatFields) | null>(null);
  const loadError = ref<string | null>(null);
  const formError = ref<string | null>(null);
  const formFieldErrors = ref<Record<string, string>>({});
  /** The server's MJML compile errors for the HTML part (one per problem). */
  const htmlErrors = ref<MjmlError[]>([]);
  /** The other format's source while the user toggles, so switching back restores it. */
  const drafts: Record<HtmlFormat, string> = { html: "", mjml: "" };
  const saving = ref(false);
  const sampleVars = ref(DEFAULT_SAMPLE_VARS);
  const previewVersion = ref<number | null>(null);
  const previewResult = ref<TemplatePreviewResult | null>(null);
  const previewError = ref<string | null>(null);
  const previewing = ref(false);

  const variableHints = computed(() => {
    const partialSources = form.value.partials.map((p) => p.source);
    return extractHbsVariables(
      form.value.subject,
      form.value.text,
      form.value.html,
      ...partialSources,
    );
  });

  /** The HTML the saved MJML compiled to (read-only, Handlebars placeholders still in place). */
  const compiledHtml = computed(() =>
    current.value && formatOf(current.value) === "mjml" ? (current.value.htmlCompiled ?? "") : "",
  );

  const resetDrafts = (): void => {
    drafts.html = "";
    drafts.mjml = "";
  };

  /**
   * Switches the HTML part between HTML and MJML. The source of the format left behind is kept for this
   * session; an MJML part with nothing to restore starts from the starter snippet.
   */
  const setHtmlFormat = (next: HtmlFormat): void => {
    const prev = form.value.htmlFormat;
    if (prev === next) return;
    drafts[prev] = form.value.html;
    const restored = drafts[next];
    form.value.html = next === "mjml" && isBlankSource(restored) ? MJML_STARTER : restored;
    form.value.htmlFormat = next;
    htmlErrors.value = [];
  };

  const versionOptions = computed(() => {
    const n = current.value?.currentVersion ?? 0;
    return Array.from({ length: n }, (_, i) => i + 1).reverse();
  });

  const load = async (id: string): Promise<boolean> => {
    start();
    loadError.value = null;
    try {
      const t = await getTemplate(id);
      current.value = t;
      form.value = formFromView(t);
      resetDrafts();
      htmlErrors.value = [];
      previewVersion.value = t.currentVersion;
      finish(true);
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      finish(false);
      return false;
    }
  };

  const resetNew = (): void => {
    current.value = null;
    form.value = emptyForm();
    previewVersion.value = null;
    previewResult.value = null;
    previewError.value = null;
    loadError.value = null;
    formError.value = null;
    formFieldErrors.value = {};
    htmlErrors.value = [];
    resetDrafts();
    finish(true);
  };

  const save = async (): Promise<TemplateView | null> => {
    saving.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    htmlErrors.value = [];
    try {
      if (current.value) {
        const updated = await patchTemplate(current.value.id, toUpdateBody(form.value));
        current.value = updated;
        form.value = formFromView(updated);
        previewVersion.value = updated.currentVersion;
        return updated;
      }
      const created = await createTemplate(toCreateBody(form.value));
      current.value = created;
      form.value = formFromView(created);
      previewVersion.value = created.currentVersion;
      return created;
    } catch (e) {
      if (e instanceof ApiProblemError) {
        formFieldErrors.value = fieldErrors(e);
        htmlErrors.value = mjmlErrorsOf(e.problem);
      }
      // Compile errors are listed at the editor; the page-level alert would only repeat the first one.
      formError.value = htmlErrors.value.length ? null : messageOf(e);
      return null;
    } finally {
      saving.value = false;
    }
  };

  const runPreview = async (): Promise<TemplatePreviewResult | null> => {
    if (!current.value) {
      previewError.value = "unsaved";
      return null;
    }
    const parsed = parseSampleVariables(sampleVars.value);
    if (!parsed.ok) {
      previewError.value = parsed.error;
      previewResult.value = null;
      return null;
    }
    previewing.value = true;
    previewError.value = null;
    try {
      const res = await previewTemplate(current.value.id, {
        version: previewVersion.value ?? undefined,
        variables: parsed.value,
        locale: form.value.locale || undefined,
        timezone: form.value.timezone || undefined,
      });
      previewResult.value = res;
      return res;
    } catch (e) {
      previewError.value = messageOf(e);
      previewResult.value = null;
      return null;
    } finally {
      previewing.value = false;
    }
  };

  return {
    form,
    current,
    loadError,
    formError,
    formFieldErrors,
    htmlErrors,
    compiledHtml,
    saving,
    sampleVars,
    previewVersion,
    previewResult,
    previewError,
    previewing,
    variableHints,
    versionOptions,
    isFirstLoad,
    isRefreshing,
    isLoading,
    load,
    resetNew,
    save,
    runPreview,
    setHtmlFormat,
  };
};
