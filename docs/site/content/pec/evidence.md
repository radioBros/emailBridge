# PEC evidence archive

When `pec.evidenceArchive` is on, EmailBridge stores every PEC receipt (and received
envelope) as legal evidence: raw `.eml` bytes plus a JSON manifest in the dedicated
evidence bucket (`EB_S3_EVIDENCE_BUCKET`), under Object Lock retention
(`pec.evidenceRetention`, default ~10 years; mode `EB_S3_EVIDENCE_LOCK_MODE`,
`GOVERNANCE` by default).

## Layout

```text
evidence/<org>/<account>/<yyyy>/<mm>/<id>.eml
evidence/<org>/<account>/<yyyy>/<mm>/<id>.json
evidence/<org>/<account>/<yyyy>/<mm>/<id>.tsr   # optional RFC 3161 token
```

The manifest holds `id`, `sha256`, `size`, `receivedAt`, `account`, `type`,
`transportId`, `signature`, and optional `tsa`. Archiving the same id with the same
hash is a no-op; a different hash is refused (evidence is never overwritten).

## Object Lock check

At start, EmailBridge checks that the evidence bucket exists and has Object Lock
enabled. If not, a health warning is raised and archiving is refused until fixed.

## RFC 3161 timestamps

Optional org setting `pec.tsaUrl`. On success the `.tsr` is stored next to the
manifest and the TSA chain is checked against `EB_PEC_TSA_ANCHORS` when set. On
failure the object is still archived without a token and a job
`evidence.tsa-retry` retries for up to 24 hours.

## Verify

`GET /v1/pec/receipts/{receiptId}/evidence` returns the manifest fields plus
`{ intact, hashMatches, locked, lockedUntil, tsa }`.

## Erasure

Account / organisation erasure removes `pecReceipts` rows but **not** locked
evidence objects (GDPR Art. 17(3)(b)/(e)). See `docs/ops/gdpr.md`.
