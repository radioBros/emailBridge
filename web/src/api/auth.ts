import { createAuthClient } from "limen-auth/vue";
import { credentialPasswordPlugin } from "limen-auth/plugins/credential";
import { twoFactorPlugin } from "limen-auth/plugins/two-factor";
import { passkey } from "limen-auth/plugins/passkey";

/**
 * Limen auth client (S13 §5). Screens in P0.22; session store uses `useSession()`.
 * Credential / MFA helpers that need RFC 9457 problem codes live in `authFlow.ts`.
 */
export const auth = createAuthClient({
  baseURL: typeof window !== "undefined" ? window.location.origin : "",
  basePath: "/v1/auth",
  fetchOptions: {
    credentials: "include",
    headers: {
      "X-Requested-With": "EmailBridge",
    },
  },
  plugins: [
    credentialPasswordPlugin(),
    twoFactorPlugin({
      onTwoFactorRedirect: () => {
        if (typeof window !== "undefined" && !window.location.pathname.startsWith("/login")) {
          window.location.assign("/login?mfa=1");
        }
      },
    }),
    passkey(),
  ],
});

export type AuthClient = typeof auth;
