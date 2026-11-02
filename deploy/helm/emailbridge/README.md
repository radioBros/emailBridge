# EmailBridge Helm chart

Large-tier install: one image, four Deployments (`api`, `sync`, `webhook`, `worker`), external MongoDB /
NATS / S3. Operations guide: `docs/ops/large.md`.

## Install

```bash
# Create the secrets Secret first (never commit values).
kubectl create secret generic emailbridge-secrets \
  --from-file=mongo_uri=./secrets/mongo_uri \
  --from-file=auth_secret=./secrets/auth_secret \
  --from-file=keys=./secrets/keys \
  --from-file=s3_access_key=./secrets/s3_access_key \
  --from-file=s3_secret_key=./secrets/s3_secret_key

helm upgrade --install emailbridge ./deploy/helm/emailbridge \
  --namespace emailbridge --create-namespace \
  --set config.EB_PUBLIC_URL=https://mail-gw.example.com \
  --set config.EB_NATS_URL=nats://nats-0:4222,nats://nats-1:4222,nats://nats-2:4222 \
  --set config.EB_S3_ENDPOINT=https://s3.eu-south-1.amazonaws.com \
  --set existingSecret=emailbridge-secrets
```

## Rules

- **No init containers.** Entrypoint takes the migration lock, migrates, declares NATS streams/KV, ensures
  the S3 bucket, then starts roles.
- `terminationGracePeriodSeconds` defaults to **75** (> `EB_SHUTDOWN_GRACE` 60s).
- Probes: `/healthz` (liveness), `/readyz` (readiness) on port 8080.
- HPA on `api` and `webhook` (CPU). PDBs on every role.
- Optional `ServiceMonitor` when Prometheus Operator is present (`serviceMonitor.enabled=true`).

## Optional subcharts

`values.yaml` has `mongodb.enabled` / `nats.enabled` / `minio.enabled` (all **false**). Prefer managed
MongoDB (Atlas), a NATS cluster you already run, and cloud S3. To vendor Bitnami/nats-io charts locally,
add them under `dependencies` in `Chart.yaml` and run `helm dependency update`.

## Lint / template

```bash
docker run --rm -v "$PWD:/work" -w /work alpine/helm:3.16.3 lint deploy/helm/emailbridge
docker run --rm -v "$PWD:/work" -w /work alpine/helm:3.16.3 template eb deploy/helm/emailbridge
```
