# Admin UI tests

Repeatable Playwright suite against a **real** EmailBridge stack (not the mocked `web/scripts/shots-*.mjs` helpers).

## Prerequisites

- MongoDB at `mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true` (or `EB_TEST_MONGO_URI`)
- Docker available for SeaweedFS S3 unless `EB_TEST_S3_ENDPOINT` + keys are set
- Built admin SPA: `cd web && pnpm build` (copied into `internal/webui/dist` by the stack helper)
- Keep resource use modest: `GOMAXPROCS=2`, Playwright `workers: 2`, one browser project serialised by shared stack

## Stack

```bash
go run ./test/uistack
```

Boots the provider simulator (PEC domain `pec.sim.test`), one EmailBridge node with roles `all`, a fresh Mongo database, and serves the embedded SPA on `http://127.0.0.1:18765` (override with `EB_UISTACK_ADDR`).

Seeds via the public API (invite plaintext token is the one exception — inserted like the e2e harness). Listens on `127.0.0.1:18765` but advertises `http://localhost:18765` (WebAuthn RP ID must be a hostname). Prints one JSON line and writes `web/e2e/.stack.json` with `baseUrl`, owner/viewer credentials (incl. TOTP secrets), and useful ids. Waits until killed.

## Suite

```bash
cd web
pnpm install
pnpm exec playwright install chromium
pnpm test:ui
```

`pnpm test:ui` runs global setup (starts `go run ./test/uistack` if `.stack.json` is missing), then four projects:

| Project | Viewport | Theme |
| --- | --- | --- |
| `phone-light` | 390×844, touch | light |
| `phone-dark` | 390×844, touch | dark |
| `desktop-light` | 1440×900 | light |
| `desktop-dark` | 1440×900 | dark |

Each screen spec checks: main content ready (no fixed sleeps), one interaction, no console errors, no unexpected failed requests, no horizontal overflow, axe with no serious/critical violations. Full-page screenshots land in `web/e2e/screens/<project>/<screen>.png` (gitignored).

`e2e/specs/layout-fit.spec.ts` checks every page with a table: on desktop the page does not scroll, the
table footer sits on the card's bottom edge, the table body ends on the footer and the card reaches the
viewport bottom (measured fill height, `composables/useFillHeight`); nowhere does the page scroll
sideways. Pages that legitimately scroll (content above or a second table) carry a `scrolls` reason in the
spec. Viewport screenshots land in `web/test-results/layout-fit/<project>/` for review.

`pnpm test:ui:update` is reserved for snapshot updates; the suite currently has no snapshot baselines.

## Report

Each failure names the screen, project, assertion and screenshot path. Do not hide failures with retries or loosened assertions.

## Env

| Variable | Meaning |
| --- | --- |
| `EB_UI_BASE_URL` | Skip stack boot; use an already running node |
| `EB_UISTACK_ADDR` | Bind address for uistack (default `127.0.0.1:18765`) |
| `EB_TEST_MONGO_URI` | Mongo URI |
| `EB_TEST_S3_*` | Reuse an existing S3-compatible endpoint |
| `EB_E2E_BIN` | Prebuilt `emailbridge` binary (must include embedweb assets) |
