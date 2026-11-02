# EmailBridge admin (web/)

Vue 3 + TypeScript + Vuetify 4 SPA. Built on the vuetify-starter-kit with MDI SVG icons,
colourful palette (S13), and the EmailBridge shell.

## Commands

```bash
pnpm install
pnpm dev          # http://localhost:5173 — proxies /v1 → :8080
pnpm lint
pnpm test
pnpm coverage     # ≥ 80 % lines
pnpm build        # → web/dist
```

## Layout

- `src/app/` — bootstrap, router, i18n, layouts
- `src/components/` — shared kit components (TableCard, AppCard, …)
- `src/features/<name>/` — screens (`routes.ts`, `pages/`, `locales/`)
- `src/api/` — Xior client + Limen auth
- `src/styles/` — SCSS tokens and helpers (`@use` only)

## Notes

- Default locale: Italian (`it`); English (`en`) also available.
- No external CDN requests at runtime (icons and validation glyphs are bundled).
- Column settings persist via `GET/PATCH /v1/me/settings`.
