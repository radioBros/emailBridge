/** RFC 9457 problem details as returned by EmailBridge. */
export type ProblemItem = {
  path: string;
  code: string;
  message: string;
};

export type Problem = {
  type: string;
  title: string;
  status: number;
  detail?: string;
  instance?: string;
  code: string;
  kind: string;
  traceId?: string;
  requestId?: string;
  errors?: ProblemItem[] | null;
};

export class ProblemError extends Error {
  readonly problem: Problem;
  readonly status: number;
  readonly headers: Headers;

  constructor(problem: Problem, headers: Headers) {
    super(problem.detail || problem.title || problem.code);
    this.name = "ProblemError";
    this.problem = problem;
    this.status = problem.status;
    this.headers = headers;
  }
}

export const parseProblem = (body: unknown, status: number): Problem => {
  if (body && typeof body === "object") {
    const o = body as Record<string, unknown>;
    return {
      type: String(o.type ?? `https://emailbridge.dev/problems/http_${status}`),
      title: String(o.title ?? "Error"),
      status: typeof o.status === "number" ? o.status : status,
      detail: o.detail != null ? String(o.detail) : undefined,
      instance: o.instance != null ? String(o.instance) : undefined,
      code: String(o.code ?? `http_${status}`),
      kind: String(o.kind ?? "temporary"),
      traceId: o.traceId != null ? String(o.traceId) : undefined,
      requestId: o.requestId != null ? String(o.requestId) : undefined,
      errors: Array.isArray(o.errors) ? (o.errors as ProblemItem[]) : null,
    };
  }
  return {
    type: `https://emailbridge.dev/problems/http_${status}`,
    title: "Error",
    status,
    code: `http_${status}`,
    kind: "temporary",
  };
};
