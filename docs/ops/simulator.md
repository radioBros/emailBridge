# Provider simulator (`emailbridge-sim`)

Fake IMAP + SMTP provider with an HTTP control API for local development and integrator
tests. Same process as the in-tree `simulator/` package used by EmailBridge’s own suites.

**Do not expose the control API on a public interface** — it is unauthenticated.

## Run

```bash
go run ./cmd/emailbridge-sim
# or after build:
go build -o emailbridge-sim ./cmd/emailbridge-sim
./emailbridge-sim
```

On start the process prints one JSON object of bound addresses to stdout, for example:

```json
{"imapPlain":"127.0.0.1:1143","imapTLS":"127.0.0.1:1993","smtpPlain":"127.0.0.1:1025","smtpTLS":"127.0.0.1:1465","control":"127.0.0.1:18080"}
```

Defaults bind localhost on fixed ports (override with flags or env). Use `:0` for an
ephemeral port (tests).

| Flag | Env | Default |
| --- | --- | --- |
| `-imap` | `EB_SIM_IMAP` | `127.0.0.1:1143` |
| `-imap-tls` | `EB_SIM_IMAP_TLS` | `127.0.0.1:1993` |
| `-smtp` | `EB_SIM_SMTP` | `127.0.0.1:1025` |
| `-smtp-tls` | `EB_SIM_SMTP_TLS` | `127.0.0.1:1465` |
| `-control` | `EB_SIM_CONTROL` | `127.0.0.1:18080` |
| `-pec-domain` | `EB_SIM_PEC_DOMAIN` | (off) enable PEC for this domain |
| `-pec-ca` | `EB_SIM_PEC_CA` | write test CA PEM (for `EB_PEC_TRUST_ANCHORS`) |
| `-tls-only` | — | prints a note about the self-signed TLS cert |

## TLS

IMAP/SMTP implicit-TLS listeners and STARTTLS use a **process-local self-signed**
certificate. Clients must set insecure-skip-verify (or pin the cert from a test harness).
There is no flag to load external cert files in this binary; production mail is never the
goal of the simulator.

## Control API (overview)

Base URL = the `control` address. JSON request/response.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Liveness |
| `GET` | `/addrs` | Bound addresses |
| `POST` | `/reset` | Clear all users/mail |
| `POST` | `/users` | `{ username, password }` |
| `DELETE` | `/users/{user}` | Remove user |
| `POST` | `/users/{user}/mailboxes` | Create mailbox |
| `POST` | `/users/{user}/mailbox/inject` | Inject a message |
| `POST` | `/users/{user}/mailbox/flags` | Set flags |
| `POST` | `/users/{user}/mailbox/expunge` | Expunge |
| `POST` | `/users/{user}/mailbox/recreate` | New UIDVALIDITY |
| `PUT` | `/users/{user}/caps` | Toggle IMAP capabilities |
| `PUT` | `/users/{user}/faults/imap` | Script IMAP faults |
| `PUT` | `/users/{user}/faults/smtp` | Script SMTP replies / drop-after-DATA |
| `GET` | `/smtp/messages` | Captured SMTP submissions |
| `POST` | `/fleet` | Start load-test fleet |
| `DELETE` | `/fleet` | Stop fleet |
| `GET` | `/pec/ca.pem` | PEC test CA PEM (PEC mode) |
| `GET` | `/pec/receipts?user=` | Generated PEC receipts |
| `PUT` | `/pec/users/{user}/delay` | `{ "delayMs": N }` delivery delay |
| `PUT` | `/pec/users/{user}/force-receipt` | `{ "type": "…" }` next receipt type |

### PEC mode

`-pec-domain pec.sim.test` turns on the PEC simulator: acceptance / delivery receipts, transport
and anomaly envelopes, signed with a process-local test CA. Fault local-parts: `nouser`,
`nodomain`, `slow`, `virus`, `badsig`, `dupe`.

Go helpers: `simulator/control` client and `simulator/simtest.Start` / `StartPEC` for in-process tests.

## Relation to `cmd/eb-simulator`

`eb-simulator` is the ephemeral-port helper used by the test suites. Prefer
`emailbridge-sim` for the documented integrator tool (stable default ports + ops guide).
