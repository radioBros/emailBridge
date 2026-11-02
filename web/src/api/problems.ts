import { ApiProblem, ApiProblemError } from "@/api/client";
import { AuthProblemError } from "@/api/authFlow";
import { LimenError } from "limen-auth";

/** Extract an RFC 9457 problem (or a close stand-in) from any thrown value. */
export const problemOf = (err: unknown): ApiProblem | null => {
  if (err instanceof AuthProblemError) return err.problem;
  if (err instanceof ApiProblemError) return err.problem;
  if (err instanceof LimenError) {
    return {
      status: err.status || 0,
      code: mapLimenStatus(err.status),
      detail: err.message,
      title: err.message,
    };
  }
  return null;
};

const mapLimenStatus = (status: number): string => {
  if (status === 401) return "invalid_credentials";
  if (status === 403) return "forbidden";
  if (status === 423) return "account_locked";
  if (status === 429) return "rate_limited";
  if (status === 422 || status === 400) return "validation_failed";
  if (status === 410) return "not_found";
  return "internal_error";
};

export const problemCodeOf = (err: unknown): string | null => problemOf(err)?.code ?? null;

export const isProblemCode = (err: unknown, code: string): boolean => problemCodeOf(err) === code;
