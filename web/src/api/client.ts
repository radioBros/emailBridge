import xior, {
  type XiorError,
  type XiorInstance,
  type XiorInterceptorRequestConfig,
  type XiorRequestConfig,
} from "xior";

/** RFC 9457 problem detail as returned by the EmailBridge API. */
export interface ApiProblem {
  type?: string;
  title?: string;
  status: number;
  detail?: string;
  instance?: string;
  code: string;
  traceId?: string;
  errors?: Array<{ path: string; message: string; code?: string }>;
  [key: string]: unknown;
}

export class ApiProblemError extends Error {
  readonly problem: ApiProblem;

  constructor(problem: ApiProblem) {
    super(problem.detail || problem.title || problem.code);
    this.name = "ApiProblemError";
    this.problem = problem;
  }
}

type OrgGetter = () => string | null | undefined;
type LoginRedirect = () => void;

let getOrganizationId: OrgGetter = () => null;
let onUnauthorized: LoginRedirect = () => {
  if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
    window.location.assign("/login");
  }
};

/** Wire session-dependent headers/redirects after the session store exists. */
export const configureApiClient = (opts: {
  getOrganizationId: OrgGetter;
  onUnauthorized?: LoginRedirect;
}): void => {
  getOrganizationId = opts.getOrganizationId;
  if (opts.onUnauthorized) onUnauthorized = opts.onUnauthorized;
};

const isProblem = (data: unknown): data is ApiProblem =>
  typeof data === "object" &&
  data !== null &&
  typeof (data as ApiProblem).code === "string" &&
  typeof (data as ApiProblem).status === "number";

const parseProblem = (error: XiorError): ApiProblem => {
  const data = error.response?.data;
  if (isProblem(data)) return data;
  const status = error.response?.status ?? 0;
  return {
    status,
    code: status === 401 ? "unauthenticated" : "internal_error",
    title: error.message,
    detail: typeof data === "string" ? data : error.message,
  };
};

const http: XiorInstance = xior.create({
  baseURL: "/v1",
  credentials: "include",
  headers: {
    Accept: "application/json",
  },
});

http.interceptors.request.use((config: XiorInterceptorRequestConfig) => {
  const headers = { ...(config.headers ?? {}) } as Record<string, string>;
  headers["X-Requested-With"] = "EmailBridge";
  const org = getOrganizationId();
  if (org) headers["EB-Organization"] = org;
  config.headers = headers;
  return config;
});

const IDEMPOTENT = new Set(["GET", "HEAD", "OPTIONS"]);

type RetryConfig = XiorRequestConfig & { __retried?: boolean };

http.interceptors.response.use(
  (response) => response,
  async (error: XiorError) => {
    const config = (error.config ?? {}) as RetryConfig;
    const method = (config.method ?? "GET").toUpperCase();
    const noResponse = !error.response;

    if (noResponse && IDEMPOTENT.has(method) && !config.__retried) {
      const retryConfig: RetryConfig = { ...config, __retried: true };
      return http.request(retryConfig);
    }

    const problem = parseProblem(error);
    if (
      problem.status === 401 &&
      (problem.code === "unauthenticated" || problem.code === "session_expired")
    ) {
      onUnauthorized();
    }
    throw new ApiProblemError(problem);
  },
);

/** Kit contract used by `stores/settings.ts`. */
export const api = {
  get: async <T>(path: string): Promise<T> => {
    const res = await http.get<T>(path);
    return res.data;
  },
  patch: async <T = unknown>(path: string, body: unknown): Promise<T> => {
    const res = await http.patch<T>(path, body);
    return res.data;
  },
  post: async <T = unknown>(path: string, body?: unknown): Promise<T> => {
    const res = await http.post<T>(path, body);
    return res.data;
  },
  put: async <T = unknown>(path: string, body?: unknown): Promise<T> => {
    const res = await http.put<T>(path, body);
    return res.data;
  },
  delete: async <T = unknown>(path: string): Promise<T> => {
    const res = await http.delete<T>(path);
    return res.data;
  },
  raw: http,
};

export default api;
