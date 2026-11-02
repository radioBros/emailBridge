# Provider simulator

`emailbridge-sim` is a fake IMAP/SMTP provider with an HTTP control API for local development
and tests. Full reference: [ops/simulator.md](https://github.com/radioBros/emailBridge/blob/main/docs/ops/simulator.md)
in the repository.

## PEC mode

Enable with `-pec-domain pec.sim.test` (or `EB_SIM_PEC_DOMAIN`). The process creates a test CA and
a signer per domain. Write the CA PEM with `-pec-ca /path/ca.pem` and point EmailBridge at it via
`EB_PEC_TRUST_ANCHORS`.

SMTP submission from a mailbox on the PEC domain yields acceptance and delivery receipts (and a
transport envelope for PEC recipients). Fault local-parts: `nouser`, `nodomain`,
`slow`, `virus`, `badsig`, `dupe`. Ordinary mail to a PEC mailbox becomes an anomaly envelope.

Control API (when PEC is on):

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/pec/ca.pem` | Test CA certificate |
| `GET` | `/pec/receipts?user=` | Generated receipts |
| `PUT` | `/pec/users/{user}/delay` | `{ "delayMs": N }` |
| `PUT` | `/pec/users/{user}/force-receipt` | `{ "type": "…" }` |
