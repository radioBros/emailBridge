# Medium tier

The medium stack (`deploy/compose/medium.yml`, project name `emailbridge`) runs a three-node NATS
JetStream cluster, a three-member MongoDB replica set, SeaweedFS for S3, and EmailBridge split by role
behind a single `gateway` reverse proxy. Use it when one box is no longer enough but you are not yet on
Kubernetes (large tier).

## 1. Install

1. Install Podman (preferred) or Docker Compose on the host. Bind mounts need a writable data directory.
2. From the repository root, create secrets (same file names as the small tier):

   ```bash
   mkdir -p secrets
   podman run --rm --userns=keep-id -v "$PWD/secrets:/secrets:Z" ghcr.io/radiobros/emailbridge:v1.0.0 secrets init --dir /secrets
   ```

3. Edit `secrets/mongo_uri` so it lists all three members and the replica set name:

   ```text
   mongodb://root:<password>@mongo-1:27017,mongo-2:27017,mongo-3:27017/?authSource=admin&replicaSet=rs0
   ```

   Use the same root password as `secrets/mongo_root_password`. Every other secret stays as
   `secrets init` wrote it (`*_FILE` in compose).

4. Create data directories (optional — Compose creates them on first start):

   ```bash
   mkdir -p .data/{nats-1,nats-2,nats-3,mongo-1,mongo-2,mongo-3,s3}
   mkdir -p .data/{emailbridge-api-1,emailbridge-api-2,emailbridge-sync-1,emailbridge-sync-2,emailbridge-worker}
   ```

5. Start the stack:

   ```bash
   podman compose -p emailbridge --project-directory . -f deploy/compose/medium.yml up -d
   # or:
   docker  compose -p emailbridge --project-directory . -f deploy/compose/medium.yml up -d
   ```

6. Wait until `gateway` is healthy, then open `http://127.0.0.1:8080`. Put TLS on `gateway` or on an outer
   reverse proxy; set `EB_PUBLIC_URL` and `EB_HTTP_TRUSTED_PROXIES` to match.

There is no init container. Mongo members initiate or join `rs0` from their own healthchecks. EmailBridge
nodes wait on `depends_on: condition: service_healthy` for NATS, Mongo and S3, then run migrations,
declare JetStream streams/KV (`EB_NATS_REPLICAS=3`) and ensure the S3 bucket in their entrypoint.

## 2. Sizing

| Role service | Default in `medium.yml` | What it does |
| --- | --- | --- |
| `emailbridge-api-1` / `emailbridge-api-2` | 2 replicas, `EB_ROLES=api,webhook` | REST, admin SPA, WebSocket, webhook router |
| `emailbridge-sync-1` / `emailbridge-sync-2` | 2 replicas, `EB_ROLES=sync` | IMAP sync; owns account leases |
| `emailbridge-worker` | 1 replica, `EB_ROLES=submit,scheduler` | Outbound send + scheduler leader |

- Each sync node has a connection budget of `EB_SYNC_MAX_ACCOUNTS` (default **2000**). With two sync
  nodes the medium tier holds about **4000** concurrent accounts before accounts stay disconnected with
  reason `capacity`. For larger loads, such as **10,000** accounts, add sync replicas (and raise host
  limits) until the number of sync replicas × `EB_SYNC_MAX_ACCOUNTS` covers the load, or raise
  `EB_SYNC_MAX_ACCOUNTS` if the host has headroom.
- Scale **api/webhook** on HTTP CPU and webhook queue depth; scale **sync** on account count and
  `EBSyncCapacity` alerts (90 % of budget); scale **submit** when the submit stream backlog grows; keep
  **one** active scheduler leader (extra worker replicas are hot standbys for the leader lease).
- Each replica must have a unique `EB_NODE_ID`. Leave it empty so the process uses the container hostname
  (`emailbridge-api-1`, `emailbridge-sync-2`, …). Do not give two containers the same hostname.

## 3. Rolling restart

1. Drain one sync (or any) node without stopping the whole stack — publish on
   `eb.cluster.drain.<nodeId>` (nodeId is the hostname, e.g. `emailbridge-sync-1`), or send SIGTERM to that
   container.
2. The node marks itself `draining` in the `eb-nodes` KV bucket. Placement moves its account and partition
   leases to other live nodes via hand-off, rate-limited by `EB_SYNC_CONNECT_RATE`, for at most
   `EB_SHUTDOWN_GRACE` (default 60s). Leftover leases are released at the deadline, not abandoned.
3. Replace or restart that container, wait until healthy, then drain the next. Always leave at least one
   healthy replica of each role when you can.
4. After a binary upgrade, the first node to start runs forward-only migrations under the migration lock;
   a binary never starts against a newer schema.

## 4. Losing a NATS or Mongo node

- **NATS:** JetStream is configured with `EB_NATS_REPLICAS=3`. Losing one of `nats-1..3` keeps a majority;
  clients reconnect via the comma-separated `EB_NATS_URL`. Bring the member back with the same bind-mounted
  store. Losing two members blocks JetStream until quorum returns — restore nodes before changing stream
  config.
- **MongoDB:** `rs0` needs a majority (two of three). One member down: reads/writes continue. Two down:
  the set is read-only / unavailable until a second member returns. Resync from a healthy member; do not
  `rs.initiate` again on a running set. Auth uses the shared `mongo_keyfile` (same pattern as the small tier).

## 5. Backup

MongoDB is the source of truth; NATS is transient; S3 holds raw outbound mail, archives and exports.
Back up like this:

- Daily `mongodump --oplog`, or filesystem snapshots of a **stopped secondary** bind mount
  (`.data/mongo-2` or `mongo-3`).
- Replicate or version the S3 bucket (SeaweedFS replication or an S3 sync tool).
- Retention 30 days. After restore, the outbox relay and scheduler republish anything still due; the
  no-replay rule prevents old mail from emitting duplicate events.
