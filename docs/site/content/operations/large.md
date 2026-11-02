# Large tier

The large tier is for multi-host / cloud installs: EmailBridge roles as separate deployments, a sharded
MongoDB cluster, a NATS JetStream cluster, and S3. Compose stops at medium (`deploy/compose/medium.yml`);
for the large tier use the Helm chart (`deploy/helm/emailbridge`) or the AWS Terraform module
(`deploy/terraform/aws`). This guide describes the operating shape of that tier.

## 1. Roles as separate deployments

Run each role as its own Deployment (or equivalent), one image, different `EB_ROLES`:

| Deployment | `EB_ROLES` | Autoscale on |
| --- | --- | --- |
| `emailbridge-api` | `api` | HTTP CPU / RPS |
| `emailbridge-webhook` | `webhook` | CPU and delivery / event partition lag |
| `emailbridge-sync` | `sync` | Account count vs `EB_SYNC_MAX_ACCOUNTS` per pod; `EBSyncCapacity` |
| `emailbridge-submit` | `submit` | Submit stream backlog / queue depth |
| `emailbridge-scheduler` | `scheduler` | Keep small (leader election); 2 replicas is enough for HA |

Every pod needs a unique `EB_NODE_ID` (Kubernetes: downward API hostname / pod name). Set
`EB_NATS_MODE=external`, `EB_NATS_REPLICAS=3` (or 5), and point `EB_NATS_URL` / `EB_MONGO_URI` /
`EB_S3_*` at the shared backing services. All secrets via `_FILE` or the platform secret store.

A single reverse-proxy / load balancer remains the cluster entry point (same idea as medium `gateway`).

## 2. Sharded MongoDB

Small and medium tiers are a replica set only. Large shards by these keys:

- Account-owned, high-volume collections: ranged shard key `{ accountId: 1, _id: 1 }` (one account stays
  together; ULID account IDs spread new accounts into the newest chunk).
- Organisation-owned collections: `{ organizationId: 1, _id: 1 }` (sharded only on the large tier).

Use a production MongoDB service (Atlas or self-managed config servers + shards + mongos). EmailBridge
still requires a replica-set capable URI (transactions). Point `EB_MONGO_URI` at mongos; keep
`EB_MONGO_DATABASE=emailbridge`.

## 3. NATS cluster sizing

- Minimum **3** JetStream nodes (`EB_NATS_REPLICAS=3`); use **5** when you need to lose two nodes and keep
  quorum.
- Streams and KV buckets are declared by the EmailBridge entrypoint (no init job). Size disk for
  `EMAILBRIDGE_EVENTS` (short retention), `EMAILBRIDGE_DELIVERY` / `EMAILBRIDGE_SUBMIT` (work queues), and
  the `eb-leases` / `eb-nodes` / `eb-cache` buckets.
- Prefer TLS + `EB_NATS_CREDS_FILE` on the large tier.

## 4. Object storage (S3)

Use a durable S3-compatible bucket (cloud S3, MinIO, or distributed SeaweedFS). Same keys as every other
tier: `EB_S3_ENDPOINT`, `EB_S3_BUCKET`, path-style or virtual-hosted, credentials via `_FILE` or an IAM
role. Enable versioning / cross-region replication for DR; evidence bucket (`EB_S3_EVIDENCE_BUCKET`) only
for PEC evidence archiving.

## 5. Backup and upgrades

Same contract as the medium tier: MongoDB is source of truth; dump with oplog or continuous backup; S3
replication; NATS is rebuilt from Mongo after restore. Rolling upgrades drain with
`eb.cluster.drain.<nodeId>` / SIGTERM within `EB_SHUTDOWN_GRACE` (see [Medium tier, section 3](medium.md)).

## 6. Packaging

- Helm chart: `deploy/helm/emailbridge`.
- Terraform: `deploy/terraform/aws` — ECS/Fargate, ALB, S3, optional CloudFront for admin assets; MongoDB
  Atlas or self-managed.

Coming from the medium compose stack, add hosts and role replicas first, then move backing services to
managed offerings.
