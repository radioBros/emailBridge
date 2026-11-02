# API

EmailBridge exposes its own REST API (OpenAPI 3.1) — it is **not** EmailEngine-compatible. A running node
serves the OpenAPI document at `/v1/openapi.yaml`; the same document is committed in the repository.

## Interactive viewer

Point any OpenAPI viewer at the document, either in the repository or on a running node:

```text
api/openapi/openapi.yaml
https://<your host>/v1/openapi.yaml
```

This site also carries a static [Redoc page](openapi.html) that renders an `openapi.yaml` placed next to it.

## Conventions

- `/v1/…` versioned paths
- RFC 9457 problem details for errors
- Correlation IDs on requests/responses
- Auth: session (admin) and API tokens / OIDC as configured
