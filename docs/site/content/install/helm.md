# Helm (large tier)

Chart: `deploy/helm/emailbridge`. Deployments: `api`, `sync`, `webhook`, `worker` (`submit,scheduler`).
HPA on api/webhook; PDBs; optional Ingress and ServiceMonitor. External MongoDB / NATS / S3 by default.

```bash
kubectl create secret generic emailbridge-secrets \
  --from-file=mongo_uri=./secrets/mongo_uri \
  --from-file=auth_secret=./secrets/auth_secret \
  --from-file=keys=./secrets/keys

helm upgrade --install emailbridge ./deploy/helm/emailbridge \
  --namespace emailbridge --create-namespace \
  --set config.EB_PUBLIC_URL=https://mail-gw.example.com \
  --set existingSecret=emailbridge-secrets
```

- **No init containers.** `terminationGracePeriodSeconds` ≥ 75.
- Probes: `/healthz`, `/readyz`.
- Full notes: `deploy/helm/emailbridge/README.md`, [large ops](../operations/large.md).
