# EmailBridge

Open-source (AGPL-3.0) mail gateway in Go: connect mailboxes (IMAP/SMTP first), send through a durable
queue, and turn mailbox changes into signed webhooks. Own REST API (OpenAPI 3.1), Standard Webhooks,
CloudEvents payloads.

## Choose an install path

| Tier | When | Guide |
| --- | --- | --- |
| Small | Single host, all roles in one container | [Compose small](install/compose-small.md) · [Quadlet](install/quadlet.md) |
| Medium | Multi-node NATS/Mongo, role split | [Compose medium](install/compose-medium.md) |
| Large | Kubernetes or AWS ECS | [Helm](install/helm.md) · [AWS Terraform](install/aws.md) |

## Next

- [Configuration](configuration/index.md) (`EB_*` keys)
- [API](api/index.md) (OpenAPI)
- [Webhooks](webhooks/index.md)
- [Operations](operations/index.md)
