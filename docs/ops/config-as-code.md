# Config as code

Declarative organisation configuration. Enable the switch `configAsCode.enabled` (global or
organisation) before using the CLI or API.

## Document

Save the configuration as YAML, for example `org.yaml`:

```yaml
apiVersion: emailbridge.dev/v1
kind: OrganizationConfig
metadata:
  organization: acme
spec:
  settings:
    webhookTimeout: 20s
  switches:
    tracking.opens: false
  webhookEndpoints:
    - name: billing
      url: https://billing.example.com/hooks/mail
      events: [message.received, message.sent]
      secret: ${secret:BILLING_WEBHOOK_SECRET}
  routes:
    - name: billing-received
      endpoint: billing
      filter: 'event.type == "message.received"'
  apiKeys:
    - name: my-app
      scopes: [events:read, messages:write]
```

Secrets are references only (`${secret:NAME}` or `${NAME}`) to the organisation's own `settings.env`
secrets. Store each one once (or from CI) with
`PATCH /v1/settings` `{"env": {"BILLING_WEBHOOK_SECRET": {"value": "…", "secret": true}}}`; at plan/apply
time the reference is resolved from that organisation only. Server environment variables and files are
never readable from a document. An undefined name fails the plan with `422 validation_failed` naming it.
Never put plaintext secrets in the file or in git.

Accounts and mailbox credentials are not managed here.

## CLI

```bash
export EB_TOKEN=ebk_…
emailbridge config export --url https://bridge.example --org org_… --token "$EB_TOKEN" > org.yaml
emailbridge config plan  -f org.yaml --url https://bridge.example --org org_… --token "$EB_TOKEN"
emailbridge config apply -f org.yaml --url https://bridge.example --org org_… --token "$EB_TOKEN" --prune
```

- `plan` / `apply` take `--prune` to delete live resources missing from the file.
- `--output json` prints machine-readable plan or job responses.
- `apply` re-plans first; if anything changed on the server since that plan, the API returns
  `409 config_drift` and nothing is applied.

## API

| Method | Path | Notes |
| --- | --- | --- |
| `GET` | `/v1/config/export` | Live document (JSON) |
| `POST` | `/v1/config/plan` | `{ document, prune }` → plan + `planHash` |
| `POST` | `/v1/config/apply` | `{ document, prune, planHash }` → `202` job `config.apply` |

Apply runs as one job, stops at the first error, and audits each successful resource change.

## GitOps tips

1. Export once, commit the YAML (placeholders only).
2. Review `config plan` in CI (non-prune) against a staging org.
3. Apply with the same file; keep secrets in the organisation's `settings.env` (secret values), pushed from
   the runner's secret store, never in the repo.
