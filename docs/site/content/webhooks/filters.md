# Webhook filters and maps

EmailBridge decides what a webhook receives with **CEL** expressions: a boolean **filter** and an optional
**map** that shapes the delivery body. Put them directly on a **webhook endpoint** (the model EmailEngine
users know: one filter function and one map function per webhook) or on a **route** (advanced fan-out of
one event to several endpoints, each with its own filter and map).

## How an event reaches an endpoint

For every enabled endpoint of the organisation, in this order:

1. **Event subscription** — the endpoint's `events` list contains the event type (or `"*"`).
2. **Structural filter** — the endpoint's `filter.accountIds` / `filter.mailboxPaths`, if set.
3. **Endpoint filter** (`rule.filter`) — CEL, must return `bool`. `false` → nothing is sent. An evaluation
   error also sends nothing and is counted (see *Observability*).
4. **Endpoint map** (`rule.map`) — CEL; when set, its result **replaces** the delivery body.

Routes targeting the endpoint are evaluated as well. When the endpoint has a filter or map **and** a route
targeting it matches the same event, only the route's delivery is created — never two sends of one event.

Endpoint filters always apply: the `rules.enabled` switch turns routes off, never an endpoint filter (a
filter restricts what an endpoint receives, so it can never fail open).

```json
PATCH /v1/webhook-endpoints/whe_…
{
  "rule": {
    "filter": "type == \"message.received\" && isInbox()",
    "map": "{\"id\": data.messageId, \"from\": from()}"
  }
}
```

Every change of the rule creates a new version: `GET …/rule/versions`, `POST …/rule/versions/{n}/restore`.
`currentVersion` is set by the server. Send `"rule": {}` to remove the rule (versions are kept).

## CEL primer

| Variable | Type | Content |
| --- | --- | --- |
| `type` | string | Event type, e.g. `message.received` |
| `event` | map | The whole CloudEvents envelope (`id`, `type`, `time`, `source`, `data`, …) |
| `data` | map | The event's `data` (fields per event type: see the event schemas) |
| `account` | map | The event's account: `id`, `externalId`, `email`, `name` — **always strings** (`""` when unset, never null) — and `metadata` |
| `account.metadata` | map | The account's own free-form metadata (set on the account in the API or the admin); **always a map**, empty when unset |
| `env` | map(string) | Organisation `settings.env` values (secret values are masked in every test output) |
| `now` | timestamp | The **event time** (not the wall clock), so tests and dry-runs are deterministic |

Account metadata: `account.metadata.crm == "x"` reads a key; keys with `.` or `-` use index syntax
(`account.metadata["crm.id"]`). Reading a key the account does not have is an evaluation error, so guard it
when not every account sets it: `has(account.metadata.crm) && account.metadata.crm == "x"` or
`"crm" in account.metadata`. Numbers are doubles (`account.metadata.tier >= 2` works). Use it in maps too:
`{"crm": account.metadata.crm}`.

Operators: `== != < <= > >= && || !`, `in`, `?:`, string methods (`contains`, `startsWith`, `endsWith`,
`matches` (RE2), `lowerAscii`, `size`), `has(data.field)`. Maps are written as JSON-like literals:
`{"a": 1, "b": data.subject}`. Filters must return `bool`; maps must return JSON (no bytes; at most 256 KiB).
Cost and time limits apply per evaluation.

## Mail-aware helpers

All helpers are pure (no network, no database) and read the event being evaluated.

### Folder and labels

| Helper | Returns | Example |
| --- | --- | --- |
| `isInbox()` | bool | `isInbox()` — IMAP path `INBOX` (any case), special-use `\Inbox`, Microsoft Graph `inbox`, **or** a Gmail message carrying the `\Inbox` / `INBOX` label even when synced from *All Mail* |
| `inFolder(pathOrSpecialUse)` | bool | `inFolder("Projects")`, `inFolder("\\Sent")` |
| `hasLabel(name)` | bool | `hasLabel("Clients")` (case-insensitive; `\System` labels are exact) |
| `labels()` | list | `"Clients" in labels()` |

### Body and subject

| Helper | Returns | Example |
| --- | --- | --- |
| `text()` | string | Plain text; HTML stripped when only HTML exists; capped at 64 KiB |
| `textContains(s)` | bool | `textContains("invoice")` (case-insensitive) |
| `textMatches(re2)` | bool | `textMatches("(?i)order #[0-9]+")` |
| `subject()` / `subjectContains(s)` / `subjectMatches(re2)` | string / bool | `subjectContains("urgent")` |

### Attachments

| Helper | Returns | Example |
| --- | --- | --- |
| `hasAttachments()` | bool | `hasAttachments()` |
| `attachments()` | list of `{filename, contentType, size, contentId, inline}` | `attachments().exists(a, a.size > 1000000)` |
| `hasAttachment(globOrRe2)` | bool | `hasAttachment("*.pdf")`, `hasAttachment("/^inv-[0-9]+\\.xml$/")` |
| `attachmentTypes()` | list | `"application/pdf" in attachmentTypes()` |

### Senders, recipients, message class

| Helper | Returns | Example |
| --- | --- | --- |
| `from()` / `fromDomain()` / `replyTo()` | string | `fromDomain() == "partner.com"` |
| `toAddresses()` / `ccAddresses()` | list | `"desk@example.com" in toAddresses()` |
| `inDomains(list)` | bool | `inDomains(["partner.com", "acme.it"])` (from, to or cc) |
| `isReply()` / `isForward()` | bool | `!isReply()` |
| `isAutoReply()` | bool | Out-of-office / `Auto-Submitted` / `X-Autoreply` / `Precedence: bulk` |
| `isBounce()` | bool | `message.bounced`, a detected bounce, or DSN markers |

### Trust, PEC, time

| Helper | Returns | Example |
| --- | --- | --- |
| `spf()` / `dkim()` / `dmarc()` | string | `dmarc() == "pass"` (`dkim()` is `pass` when any signature passed) |
| `spamScore()` | double | `spamScore() < 5.0` (Rspamd, `0` when not scanned) |
| `pecType()` / `isPecReceipt()` | string / bool | `pecType() == "posta-certificata"` |
| `localTime(tz)` / `hourOf(ts)` / `weekdayOf(ts)` | timestamp / int | `hourOf(localTime("Europe/Rome")) < 8` (`weekdayOf`: 0 = Sunday) |
| `businessHours(tz, "09:00-18:00", "Mon-Fri")` | bool | `!businessHours("Europe/Rome", "09:00-18:00", "Mon-Fri")` |

### Named lists

Organisation lists of addresses, domains or keywords, managed by API (`/v1/rule-lists`, audited; up to 50
lists of 5 000 entries) and used in any rule:

```cel
inList("vip", fromDomain())
```

Changing a list takes effect within a few seconds, without editing any rule.

### Map helpers

| Helper | Example |
| --- | --- |
| `merge(a, b)` — keys of `b` win | `merge(event, {"vip": inList("vip", fromDomain())})` |
| `pick(map, keys)` | `pick(data, ["messageId", "subject"])` |
| `omit(map, keys)` | `omit(data, ["headers", "text"])` |

## What the sync fetches for your rules

A rule can only test data that the event carries. EmailBridge reads your filters and maps and makes the sync
fetch what they reference, for the whole organisation, even if no endpoint payload option asks for it:

| Referenced | Fetched |
| --- | --- |
| `data.headers` as a whole (e.g. `"headers": data.headers`) | **all** headers, lower-cased name → list of values |
| `data.headers["x-a"]`, `data.headers.priority`, `header("x-a")` | those headers (`header()` with a computed name: all) |
| `text()`, `textContains`, `textMatches`, `data.text` | the plain-text body |
| `attachments()`, `hasAttachment…`, `data.attachments` | attachment metadata |
| `isAutoReply()`, `isReply()`, `isBounce()` | the headers they read |

**Cost:** body and all-header fetches make every new message bigger to sync and store. Prefer a specific
header over `data.headers`, and a structural check over `text()`, when you can.

## Testing a rule

- `POST /v1/webhook-endpoints/{id}/rule/test` (and `POST /v1/routes/{id}/test`) with one of
  `{ "event": {…} }`, `{ "eventId": "evt_…" }` (a stored event of your organisation) or
  `{ "sample": "message.received" }` (generated from the event type's schema). Returns
  `{ matched, mapped, cost, durationMs, error?, trace }`; `trace` gives the result of every top-level
  `&&` / `||` term, so *why didn't it match* is visible.
- Add `filter`, `map` and / or `builder` to the same body to test a **draft** instead of the saved rule
  (nothing is saved; works on an endpoint without a rule). A compile error answers with `error` and
  `errorAt: { path, line, column }`.
- `GET /v1/routes/test-events?type=…&accountId=…` lists your organisation's recent events for the picker.
- `POST /v1/webhook-endpoints/{id}/rule/dry-run` runs the rule (or a draft `filter` / `map` in the body)
  over up to 24 h of stored events as a job: matched count, samples, errors, cost. Nothing is delivered.

## Observability

Per endpoint rule and per route: evaluated / matched / skipped / errors, last match, last error
(`GET …/rule/stats`, `GET /v1/routes/{id}/stats`) and the last 50 decisions with event id and outcome
(`GET …/rule/decisions`). Prometheus: `eb_rule_evaluations_total{org, kind, result}` (`kind` is `route` or `endpoint`; `result` is `matched`, `skipped` or `error`).

## The builder

The no-code builder (admin) supports nested `all` / `any` / `not` groups and fields for the helpers above
(folder / Inbox, label, body, attachment name / type / size, from / to domain, named list, reply / auto-reply
/ bounce, SPF / DKIM / DMARC, spam score, PEC type, size, weekday / hour / business hours in a time zone,
account), plus **any payload field** of the event schemas (`data.pec.type`, `data.size`, `data.labels` …).
It writes ordinary CEL; editing the CEL by hand switches the rule to code mode.

## In the admin: Filter & transform

Every webhook endpoint (row action, or the section in the endpoint form) and every route has a
**Filter & transform** page:

- **When to send**: the guided builder (every condition is a dropdown: event types with descriptions, the
  account, the account's real folders with *Inbox (any provider)*, Gmail labels, named lists, time zones)
  or code (CEL with highlighting, autocomplete of variables, payload fields, helpers and `env.` secrets,
  inline errors, snippets, format and full screen). The generated CEL shows under the builder.
- **What to send**: the payload as a tree (event field, constant, secret, helper, expression, nested
  object or list; start from the full event, data only, a slim body or empty) or as code.
- **Presets**: inbound mail with shared key, send status with shared key (they ask for the secret's *name*, e.g.
  `webhookKey`, and can store its value write-only), new mail in the Inbox, PEC receipts, attachments,
  bounces, no auto-replies, slim payload.
- **Test**: recent real events of your organisation, pasted JSON or a sample; the draft is evaluated live
  (MATCH / NO MATCH, each condition with its result, the delivered body, cost and time) before you save.
- **In production**: counters, the last 50 decisions (test with any of them in one click) and versions with
  their changes and restore.

Named lists are managed under **Named lists** (`/rule-lists`): search, paste or import a CSV.

## Coming from EmailEngine filter / map functions

| EmailEngine | EmailBridge |
| --- | --- |
| Filter function on the webhook (`return true` → send) | `rule.filter` on the endpoint (CEL `bool`) |
| Map function on the webhook (`return` the payload) | `rule.map` on the endpoint (CEL value) |
| `payload.event` (`messageNew`, `messageSent`, …) | `type` with EmailBridge event types (`message.received`, `message.sent`, …) |
| `payload.account` (the account name you chose) | `account.externalId` (set it to the old account name), `account.id` (`acc_…`) |
| `payload.path` / Gmail labels | `isInbox()` / `inFolder()` / `hasLabel()` / `labels()` |
| `payload.data` | `data` (EmailBridge's field names, see the event schemas) |
| `payload.date` | `event.time` |
| A secret written inside the function | a **secret** `settings.env` value, read as `env.<name>` |

Example — new mail in the Inbox of one account:

```cel
type == "message.received" && account.externalId == "testaccount" && isInbox()
```

### Example 1 — new messages in the Inbox (shared key + slim body)

EmailEngine: the filter sends when the event is a new message and the folder path, lower-cased, is `inbox`;
the map returns `{ key: <shared secret>, body: { id, date, account, event, headers, emailid, subject } }`.

EmailBridge — endpoint with events `["message.received"]` (or `["*"]`: the filter checks the type anyway):

```cel
// rule.filter
type == "message.received" && isInbox()
```

```cel
// rule.map
{
  "key": env.webhookKey,
  "body": {
    "id": data.messageId,
    "date": event.time,
    "account": account.externalId != "" ? account.externalId : account.id,
    "event": "messageNew",
    "headers": data.headers,
    "emailid": data.messageIdHeader,
    "subject": data.subject
  }
}
```

- **The key is a secret.** Store it once as a secret organisation env value named `webhookKey`
  (`PATCH /v1/settings?scope=organization` with `{"env": {"webhookKey": {"value": "…", "secret": true}}}`).
  It is never shown back and is masked (`••••`) in every test and dry-run output; only real deliveries carry
  it.
- **`"event": "messageNew"`** keeps the old event name so the consumer does not change.
- **`account`**: set each account's `externalId` to the account name EmailEngine used; the ternary falls back
  to EmailBridge's `acc_…` id when it is empty. `account.externalId` is always a string, so the ternary is safe.
- **`isInbox()`** also matches Gmail mail that carries the Inbox label while it is synced from another folder,
  which a plain `path == "inbox"` check missed. Mail in any other folder is skipped.
- **`id`** is now EmailBridge's message id (`msg_…`), usable with `/v1/accounts/{id}/messages/{messageId}`.
  The RFC 5322 Message-ID stays in `emailid`.
- **`headers`**: referencing `data.headers` as a whole makes the sync fetch **all** headers for this
  organisation: an object of lower-cased header names, each a list of values
  (`{"subject": ["…"], "received": ["…", "…"]}`).
- **`date`** is the event time (RFC 3339, UTC). For the message's own `Date:` header use `data.date`.

### Example 2 — outbound status (shared key, its own endpoint URL)

EmailEngine: send when the event is *message sent*, *message failed* (gave up) or *message delivery error*
(temporary, will retry); map `{ key: <secret>, body: <whole payload> }`.

EmailBridge — a second endpoint with events `["message.sent", "message.failed",
"message.delivery_attempt_failed"]` and no filter (the subscription does the selection; as code it would be
`type in ["message.sent", "message.failed", "message.delivery_attempt_failed"]`):

```cel
// rule.map
{"key": env.webhookKey, "body": event}
```

`body` is **EmailBridge's event**, not EmailEngine's payload, so the consumer has to read these fields:

| Field | In | Meaning |
| --- | --- | --- |
| `body.type` | all | `message.sent` / `message.delivery_attempt_failed` / `message.failed` |
| `body.id`, `body.time` | all | Event id (`evt_…`, unique — use it to de-duplicate) and time |
| `body.data.outboundId` | all | The queued message (`out_…`) returned by the send API |
| `body.data.accountId`, `body.data.externalId` | all | The sending account (`externalId` may be null here) |
| `body.data.messageIdHeader`, `body.data.subject`, `body.data.envelope` | all | Message-ID, subject, SMTP envelope (`from`, `to`) |
| `body.data.attempt`, `body.data.sentAt`, `body.data.smtp` | sent | Attempt number, time, final SMTP reply `{host, code, enhanced, response}`; `rejected` lists refused recipients |
| `body.data.attempt`, `body.data.maxAttempts`, `body.data.nextAttemptAt` | delivery_attempt_failed | The failed attempt and when the next one runs |
| `body.data.attempts`, `body.data.outcomeUnknown` | failed | Attempts made; `outcomeUnknown` when the server never answered after DATA |
| `body.data.error` | delivery_attempt_failed, failed | `{code, kind, stage, smtpCode, enhanced, response}` — the error kind and the SMTP reply |

Related: **`message.bounced`** arrives later, asynchronously, when a delivery status notification (DSN)
for a sent message comes back; add it to the endpoint's events if the consumer tracks bounces.

### Gmail

With a Gmail account, messages in the Inbox are reported from the `INBOX` folder; `isInbox()` also accepts the
`\Inbox` label on messages reported from another folder. Write `isInbox()` instead of comparing the path.
