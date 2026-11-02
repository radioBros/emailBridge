# Loki labels for EmailBridge stdout mirror

Recommended low-cardinality labels when shipping EmailBridge JSON logs to Loki:

| Label | Source | Example |
| --- | --- | --- |
| `service` | static | `emailbridge` |
| `role` | `EB_ROLES` / process role | `api`, `sync`, `submit` |
| `node` | hostname / `EB_NODE_ID` | `eb-1` |
| `env` | `EB_ENV` | `production` |

Do **not** promote account IDs, message IDs, or email addresses to labels (cardinality).
Those belong in the log line / structured fields only.
