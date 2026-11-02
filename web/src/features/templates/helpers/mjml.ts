/** MJML helpers for the template editor (G2): the starter snippet and the server's compile errors. */

import type { ApiProblem } from "@/api/client";

// local until schema regen (G2)
export type HtmlFormat = "html" | "mjml";

// local until schema regen (G2): the fields G2 adds to the template API.
export type TemplateFormatFields = {
  htmlFormat?: HtmlFormat;
  htmlCompiled?: string;
};

/** The format of a template's HTML part; templates saved before G2 carry none and are HTML. */
export const formatOf = (t: TemplateFormatFields): HtmlFormat => (t.htmlFormat === "mjml" ? "mjml" : "html");

/** Problem path of the HTML part. */
export const HTML_FIELD = "body.html";

/** Inserted when an empty HTML part switches to MJML: a responsive one-column mail with a button. */
export const MJML_STARTER = `<mjml>
  <mj-head>
    <mj-attributes>
      <mj-all font-family="Helvetica, Arial, sans-serif" />
      <mj-text font-size="15px" line-height="1.6" color="#1f2937" />
    </mj-attributes>
  </mj-head>
  <mj-body background-color="#f4f5f7">
    <mj-section background-color="#ffffff" padding="32px 24px">
      <mj-column>
        <mj-text font-size="20px" font-weight="700">Hello {{default name "there"}},</mj-text>
        <mj-text>Write your message here. Every {{variable}} is filled in when the mail is sent.</mj-text>
        <mj-button href="https://example.com/{{url id}}" background-color="#2563eb" border-radius="8px">
          Open
        </mj-button>
      </mj-column>
    </mj-section>
  </mj-body>
</mjml>
`;

export type MjmlError = {
  message: string;
  /** 1-based line in the MJML source, when the compiler named one. */
  line?: number;
};

const LINE = /^Line (\d+)\b/;

/** One entry per compile problem the server reported on the HTML part (in order, duplicates kept). */
export const mjmlErrorsOf = (problem: ApiProblem | null | undefined): MjmlError[] =>
  (problem?.errors ?? [])
    .filter((e) => e.path === HTML_FIELD && Boolean(e.message))
    .map((e) => {
      const m = LINE.exec(e.message);
      return m ? { message: e.message, line: Number(m[1]) } : { message: e.message };
    });

/** True when a source is blank, so switching it to MJML may replace it with the starter. */
export const isBlankSource = (src: string): boolean => {
  const stripped = src
    .replace(/<p>\s*(<br\s*\/?>)?\s*<\/p>/gi, "")
    .replace(/&nbsp;/gi, "")
    .trim();
  return stripped === "";
};
