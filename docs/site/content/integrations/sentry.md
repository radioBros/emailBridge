# Sentry / GlitchTip

Instance-wide error reporting for any Sentry-compatible DSN (Sentry or GlitchTip).

**Install gate:** only the **instance owner** may install this connector (other organisations →
`403 forbidden`, `reason=instance_owner_required`).

**Env vs connector:**

| Source | Behaviour |
| --- | --- |
| `EB_SENTRY_DSN` set | Always wins. Connector install/config is ignored for the reporter. |
| `EB_SENTRY_DSN` empty | An installed, enabled connector supplies DSN (encrypted secret), `environment` and `sampleRate` at runtime. Disable or uninstall turns reporting off without a restart. |

Related env (when DSN is set via env): `EB_SENTRY_ENVIRONMENT`, `EB_SENTRY_SAMPLE_RATE`,
`EB_SENTRY_SEND_PII`, `EB_SENTRY_RELEASE`. Redaction rules of the built-in reporter apply unchanged
(no request bodies, cookies, or Authorization headers).

**Data leaving EmailBridge:** redacted error reports to the DSN host.

Switch: `integrations.sentry`.
