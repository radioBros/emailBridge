# Observability (metrics, alerts, Grafana)

EmailBridge exposes Prometheus metrics on `EB_METRICS_ADDR` (default empty = off). When set (e.g.
`:9090`), the process serves only `GET /metrics` from a private registry.

## Scrape

Point Prometheus at each node:

```yaml
scrape_configs:
  - job_name: emailbridge
    metrics_path: /metrics
    static_configs:
      - targets: ["emailbridge:9090"]
```

Low-cardinality labels only: never scrape account IDs, message IDs or email addresses as series labels.
Account state and breaker gauges are refreshed from MongoDB every 30 s; counters (errors, auth failures,
dead letters, lease changes) increment at the event.

## Alert rules

Shipped rules live in `deploy/observability/alerts.yml` (`EBAuthFailuresRising`, `EBDeadLetters`,
`EBEndpointFailing`, `EBLeaseChurn`, `EBRelayLag`, `EBLogLoss`, `EBLogDiskNearCap`, `EBSyncCapacity`,
`EBMigrationsPending`, `EBInsecureTLSAccounts`, `EBRuleFailing`).

Load them with Prometheus:

```yaml
rule_files:
  - /etc/prometheus/emailbridge-alerts.yml
```

Validate (optional):

```bash
docker run --rm -v "$PWD/deploy/observability:/rules:ro" prom/prometheus:v3.5.0 \
  promtool check rules /rules/alerts.yml
```

## Grafana dashboard

Import `deploy/observability/grafana/emailbridge.json`. Choose the Prometheus datasource when prompted
(`${DS_PROMETHEUS}`). Panels: accounts by state, sync accounts / unplaced per node, relay lag, dead
letters, breaker open, errors by kind, log records / drops, JetStream stream pending.

## JetStream gauges

`eb_stream_pending`, `eb_stream_ack_pending` and `eb_stream_redeliveries` report the backlog of each
JetStream stream.
