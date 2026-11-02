# Medium tier (Compose)

Three-node NATS JetStream, three-member MongoDB replica set, role-split EmailBridge replicas, single
`gateway` entry point. File: `deploy/compose/medium.yml`. Ops: [Medium tier](../operations/medium.md).

```bash
emailbridge secrets init
# Set secrets/mongo_uri to all three members with replicaSet=rs0
podman compose -p emailbridge --project-directory . -f deploy/compose/medium.yml up -d
```

Set `EB_NATS_MODE=external`, `EB_NATS_REPLICAS=3`. Drain with SIGTERM / `eb.cluster.drain.<nodeId>` within
the 75 s stop grace.
