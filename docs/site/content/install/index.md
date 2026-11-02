# Install overview

EmailBridge always talks to **external** NATS, MongoDB (replica set), and S3-compatible storage. The same
binary runs every role (`api`, `sync`, `submit`, `webhook`, `scheduler`); you choose how many processes and
which `EB_ROLES` each one has.

Deployment basics:

- Podman-first, Compose-compatible; bind mounts; **no init containers** (migrations run in the entrypoint).
- Secrets via `EB_*_FILE` (or platform secret stores).
- Drain on SIGTERM: allow ≥ 75 s stop grace (`EB_SHUTDOWN_GRACE` default 60 s).

| Path | Path in repo |
| --- | --- |
| Small Compose | `deploy/compose/small.yml` |
| Medium Compose | `deploy/compose/medium.yml` |
| Quadlet | `deploy/quadlet/` |
| Helm | `deploy/helm/emailbridge/` |
| AWS ECS | `deploy/terraform/aws/` |
| Containerfile | `deploy/container/Containerfile` |
