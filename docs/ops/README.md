# Operations guides

Install and run EmailBridge by tier. Compose files live under `deploy/compose/`; Quadlet units under
`deploy/quadlet/`.

| Guide | Tier | What it covers |
| --- | --- | --- |
| [medium.md](medium.md) | Medium | NATS ×3, MongoDB rs ×3, role-split EmailBridge, gateway, sizing, drain, node loss |
| [large.md](large.md) | Large | Separate role deployments, sharding, NATS/S3 sizing; Helm / Terraform |
| [observability.md](observability.md) | All | Prometheus scrape (`EB_METRICS_ADDR`), alert rules, Grafana dashboard |
| [config-as-code.md](config-as-code.md) | All | YAML plan/apply/export (`emailbridge config`, `/v1/config/*`) |
| [simulator.md](simulator.md) | Dev | Provider simulator (`emailbridge-sim`): IMAP/SMTP + control API |
| [ui-tests.md](ui-tests.md) | Dev | Admin UI Playwright suite (`go run ./test/uistack`, `pnpm test:ui`) |
| [gdpr.md](gdpr.md) | All | Erasure jobs, collection registry, `GET /v1/me/export` |
| [Quadlet](../../deploy/quadlet/README.md) | Small (Podman) | Rootless systemd units, secrets, linger, auto-update |

The small tier compose file is `deploy/compose/small.yml` (all roles in one container + external NATS,
Mongo, SeaweedFS). For the small tier, follow the comments at the top of `small.yml`.
