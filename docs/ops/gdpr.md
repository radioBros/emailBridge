# GDPR operations (erasure and export)

EmailBridge is designed so the operator can meet erasure and portability duties without custom scripts.

## Erasure

| Subject | How | Job / effect |
| --- | --- | --- |
| Account | `DELETE /v1/accounts/{accountId}` | Job `erasure` — all account-classified Mongo collections, S3 prefixes, protocol/logs; then `account.deleted` |
| Organisation | `DELETE /v1/organizations/{organizationId}` | Job `organization.erasure` — every account first, then org collections, org settings, orphan `userSettings`, audit scrub |
| User | delete user (admin or self-service) | Memberships, `userSettings`, Limen sessions/passkeys/TOTP; audit keeps `usr_…` only |

Anti-drift: `internal/tenancy/collections.go` must list every Mongo collection. A new collection without a
classification fails `TestCollectionRegistryComplete`.

**Do not** delete `settings` `_id=global` or `_id=queues`, `migrations`, `locks`, or `oidcProviders` when
erasing an organisation. Backups are not rewritten; they expire with your backup retention.

### PEC evidence

Erasure deletes `pecReceipts` rows and related index data, **not** objects in
`EB_S3_EVIDENCE_BUCKET` under Object Lock. Those objects are kept under a legal
obligation (GDPR Art. 17(3)(b) and (e)). The erasure job records the keys left in
place; a bucket lifecycle rule removes them when retention (`pec.evidenceRetention`,
default ~10 years) ends.

## Portability — `GET /v1/me/export`

Signed-in **users** only (API keys receive `403`). Rate-limited with `EB_RATE_LOGIN` (default 10/min per
client IP). Audited as `me.export`.

Returns: profile, memberships, `userSettings`, active sessions (metadata only — **no tokens**), passkey
names, and up to **200** of the caller's own audit rows (newest first). TOTP secrets, password hashes,
backup codes and session tokens are never included.

Mailbox content is **not** in this payload — use the per-account bulk export (`/v1/accounts/{accountId}/exports`) for that.

## Checks

```text
go test ./internal/tenancy/ ./internal/accounts/
go test -tags integration ./internal/tenancy/ ./internal/accounts/
```

Integration names: `TestIntegrationOrganizationErasure`, `TestIntegrationAccountErasureComplete`,
`TestIntegrationMeExport`.
