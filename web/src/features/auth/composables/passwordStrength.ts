/** Simple strength hint for invite / enrolment passwords (min 12, NIST-style). */
export type PasswordStrength = "empty" | "too_short" | "weak" | "ok" | "strong";

export const passwordStrengthOf = (value: string): PasswordStrength => {
  if (!value) return "empty";
  if (value.length < 12) return "too_short";
  let score = 0;
  if (value.length >= 14) score += 1;
  if (value.length >= 20) score += 1;
  if (/[a-z]/.test(value) && /[A-Z]/.test(value)) score += 1;
  if (/\d/.test(value)) score += 1;
  if (/[^A-Za-z0-9]/.test(value)) score += 1;
  if (score >= 4) return "strong";
  if (score >= 2) return "ok";
  return "weak";
};

export const passwordStrengthColor = (s: PasswordStrength): string => {
  if (s === "strong") return "success";
  if (s === "ok") return "info";
  if (s === "weak" || s === "too_short") return "warning";
  return "secondary";
};
