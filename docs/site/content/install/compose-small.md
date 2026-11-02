# Small tier (Compose)

All EmailBridge roles in one container plus NATS, MongoDB, and SeaweedFS S3.

```bash
emailbridge secrets init   # creates ./secrets/* (0600)
podman compose -p emailbridge --project-directory . -f deploy/compose/small.yml up -d
# or: docker compose -p emailbridge --project-directory . -f deploy/compose/small.yml up -d
```

- Data under `${STACK_DATA:-./.data}` (bind mounts, `:Z` for SELinux).
- Loopback publish of `:8080`; put Traefik/nginx in front.
- Health: `/emailbridge healthcheck` → `/readyz`.

Authoritative file: `deploy/compose/small.yml` (see the comments at its top).
