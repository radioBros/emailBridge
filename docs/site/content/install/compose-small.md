# Small tier (Compose)

All EmailBridge roles in one container plus NATS, MongoDB, and SeaweedFS S3.

```bash
mkdir -p secrets
podman run --rm --userns=keep-id -v "$PWD/secrets:/secrets:Z" ghcr.io/radiobros/emailbridge:v1.0.0 secrets init --dir /secrets
# Docker: docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/secrets:/secrets" ghcr.io/radiobros/emailbridge:v1.0.0 secrets init --dir /secrets
podman compose -p emailbridge --project-directory . -f deploy/compose/small.yml up -d
# or: docker compose -p emailbridge --project-directory . -f deploy/compose/small.yml up -d
```

- Data under `${STACK_DATA:-./.data}` (bind mounts, `:Z` for SELinux).
- Loopback publish of `:8080`; put Traefik/nginx in front.
- Health: `/emailbridge healthcheck` → `/readyz`.

Authoritative file: `deploy/compose/small.yml` (see the comments at its top).
