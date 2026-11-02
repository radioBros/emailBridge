# Operations

| Guide | Content |
| --- | --- |
| [Medium](medium.md) | Role split, drain, node loss |
| [Large](large.md) | Separate deployments, sharding, Helm/Terraform pointers |
| [Observability](observability.md) | Metrics, alerts, Grafana |

Backup / restore: MongoDB is source of truth; NATS is rebuilt from Mongo after restore; S3 holds
raw mail and archives. Rolling upgrades: SIGTERM → drain → forward-only migrations under the lock.
