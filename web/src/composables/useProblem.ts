import { useI18n } from "vue-i18n";
import { ApiProblemError, type ApiProblem } from "@/api/client";

/** Map RFC 9457 problem → i18n message + field errors (S13 §3). */
export const useProblem = () => {
  const { t, te } = useI18n();

  const messageOf = (problem: ApiProblem | ApiProblemError | unknown): string => {
    const p =
      problem instanceof ApiProblemError
        ? problem.problem
        : (problem as ApiProblem | null | undefined);
    if (!p || typeof p !== "object" || !("code" in p)) {
      return t("problems.internal_error");
    }
    const key = `problems.${p.code}`;
    // Prefer the active locale; if a partial locale pack omitted the code, fall back to English
    // (never to another language's leftover via navigator-resolved locale before settings load).
    if (te(key)) return t(key);
    if (te(key, "en")) return t(key, {}, { locale: "en" });
    return p.detail || p.title || t("problems.internal_error");
  };

  const fieldErrors = (
    problem: ApiProblem | ApiProblemError | unknown,
  ): Record<string, string> => {
    const p =
      problem instanceof ApiProblemError
        ? problem.problem
        : (problem as ApiProblem | null | undefined);
    const out: Record<string, string> = {};
    for (const err of p?.errors ?? []) {
      if (err.path) out[err.path] = err.message;
    }
    return out;
  };

  return { messageOf, fieldErrors };
};
