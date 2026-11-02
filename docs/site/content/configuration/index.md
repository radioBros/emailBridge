# Configuration

Summary of the boot and runtime keys:

- Precedence: built-in default → `EB_*` / `EB_*_FILE` → MongoDB runtime settings (where allowed).
- Secrets: use `_FILE` (or ECS/K8s secret injection). Never commit secrets.
- Unknown `EB_*` keys → startup warning; invalid values → startup error (secret values never logged).

## Essentials

| Key | Notes |
| --- | --- |
| `EB_ROLES` | `all` or `api,sync,submit,webhook,scheduler` |
| `EB_PUBLIC_URL` | Required for `api` |
| `EB_MONGO_URI` | Replica set URI (secret) |
| `EB_NATS_MODE` | `embedded` or `external` |
| `EB_NATS_URL` | Required when external |
| `EB_S3_ENDPOINT` / `EB_S3_BUCKET` | Object storage |
| `EB_KEYS_LOCAL` | Field-encryption key ring when provider=`local` |
| `EB_AUTH_SECRET` | Limen secret |
| `EB_SHUTDOWN_GRACE` | Default `60s` — platform stop grace must be higher |

### Gmail Pub/Sub push

| Key | Notes |
| --- | --- |
| `EB_GMAIL_PUSH_SERVICE_ACCOUNT` | Email of the Pub/Sub push subscription's service account. **Required when `EB_ENV=production`** — otherwise `/callbacks/gmail` refuses with 401. Optional in development/staging (OIDC audience check only). |

Feature switches have safe light defaults. Change them in the admin Settings screen or with `PATCH /v1/settings`.

### Full-text search across accounts (optional)

Switch `search.fullText` (organisation and account, default **off**). Turn it on for the organisation in
**Settings → Feature switches** (or `PATCH /v1/settings` with `{"switches": {"search.fullText": {"value": true}}}`).
From then on every new message of every account is indexed (subject, sender, the first 64 KiB of the body text);
the **Search** page (Mail → Search) and `GET /v1/messages/search?q=…` find mail across all accounts, with
`"exact phrases"` and `-excluded` words. An account can opt out by setting the switch to `false` on the account.

Mail that arrived before the switch was on is added with **Reindex full-text** in the account's Settings tab (or
`POST /v1/accounts/{id}/fulltext/reindex`, up to 5000 recent messages by default). Bodies are fetched one at a time
per account, so the account stays usable.

The index is a copy of message text inside EmailBridge's own database: rows are deleted with the message, the
folder and the account, and removed when the switch is turned off (an account opt-out at once, the organisation
within 15 minutes).

### Semantic search and "Ask the mailbox" (optional)

Switch `ai.embeddings` (organisation and account, default **off**; an account can opt out like full-text search).
It finds mail **by meaning** in one account ("when is the next assembly?") and answers questions about a mailbox,
naming the messages the answer is based on. It needs, for the account: full-text search on (its text is what gets
embedded), the AI plugin (`ai.enabled`) on, and an embeddings endpoint in **Settings → AI provider → Embeddings**:

- with the **OpenAI** provider nothing more is needed (its key and `text-embedding-3-small` are used; set another
  model if you like);
- with a **local** OpenAI-compatible server (Ollama, LM Studio) set the embedding model (for example
  `nomic-embed-text`);
- with **Google Gemini** pick the embedding model (**Load embedding models** lists them; the Gemini key is reused);
- with **Anthropic** or **xAI** (no embeddings API) set an own embeddings base URL, model and, if needed, key.

From then on new mail is embedded as it is indexed. Older indexed mail is added with **Embed mail** in the account's
Settings tab (`POST /v1/accounts/{id}/embeddings/reindex`). On the **Search** page pick the account, switch the
toggle to **Meaning**, or open **Ask the mailbox**. API: `GET /v1/accounts/{id}/semantic-search?q=…`,
`POST /v1/accounts/{id}/ask` with `{"question": "…"}`, status `GET /v1/accounts/{id}/embeddings`.

Message text (with e-mail addresses hashed while PII redaction is on) goes to the embeddings provider you set, and
questions with the matching excerpts to your main AI provider; choose a local server to keep everything in-house.
The vectors are stored in EmailBridge's database and removed with the message, the account, an opt-out, the switch
turned off, or a change of embedding model. One query compares at most 50 000 chunks of an account
(`EB_AI_EMBEDDINGS_MAX_CHUNKS`): narrow very large mailboxes with a date range. Background embedding is limited to 60
requests per minute per organisation and node (`EB_AI_EMBEDDINGS_RPM`).

### AI provider (optional)

**Settings → Feature switches → AI provider** chooses the provider of EmailBridge's own AI features (summary,
classification, extraction, risk, Ask the mailbox): **OpenAI**, **Anthropic**, **Google Gemini**, **xAI (Grok)** or
**Custom / local** (any OpenAI-compatible server: Ollama, LM Studio, Azure-style gateways — set its base URL);
**None** turns the features off. Paste the provider's API key and press **Connect**: EmailBridge checks the key with
the provider and fills the model list; pick a model and save. Keys are stored encrypted, one per provider, and never
shown again; switching provider never sends a key to another provider. The features also need the `ai.enabled`
switch.

### MCP server (optional)

AI assistants (Claude Code, Claude Desktop, Cursor, VS Code) can use EmailBridge through the Model Context Protocol
at `https://<your host>/v1/mcp`. In **Settings → Feature switches → MCP server**: turn on MCP for the organisation,
then create a key on **Organisation → API keys** with the **MCP (`mcp:use`)** scope plus the scopes the assistant
may use (for example `messages:read`), and paste one of the ready-made client configurations shown in the panel.
Every API operation is available as a tool; add `?toolsets=messages,sending` to the URL to offer only some groups.
The instance owner can also open a **dedicated port** (for example `8090`); it is applied without a restart and
serves only MCP, over plain HTTP — publish it through your reverse proxy with TLS, or bind it to `127.0.0.1`.
