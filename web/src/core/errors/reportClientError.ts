/**
 * SPA client-error sink (W77). Backend Sentry/GlitchTip is wired in Go; until a browser SDK
 * is added, console is the durable local path so unhandled errors are never silent.
 */
export const reportClientError = (err: unknown, context = "unhandled"): void => {
  console.error(`[emailbridge] ${context}`, err);
};
