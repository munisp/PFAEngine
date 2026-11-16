# member-pwa — member-facing PWA (Tranche 11)

Mobile-first progressive web app for RSA members, talking to `cmd/member-svc`
(`POST /api/v1/members`, `GET /api/v1/members/{id}/balance`,
`GET /api/v1/members/{id}/statement`, `POST /api/v1/statements/verify`).
Hand-rolled PWA — no `vite-plugin-pwa` dependency; a plain service worker
(`public/sw.js`) + web app manifest.

## Features

- **Sign in** with RSA PIN + phone (PIN is the member ID; a balance read
  validates it) and **onboarding** via the KYC-gated `POST /api/v1/members`
  (full name, DOB, phone, NIN, optional BVN), tenant-scoped with the
  `X-Tenant-ID` header.
- **Dashboard**: balance card with naira formatting (`Intl.NumberFormat`
  `en-NG`, integer kobo — no floats), fund code badge from statement
  holdings, last contribution, and an offline indicator when the service
  worker served the last-known balance from CacheStorage (`X-Pwa-Cache: hit`
  response header) or the browser is offline.
- **Statement**: statement payload + HMAC verify code (FR-501), rendered as
  a QR code (`qrcode` npm package) with a text fallback, and a one-tap
  verify round-trip against `POST /api/v1/statements/verify`.
- **I-Am-Alive**: selfie capture via `getUserMedia` with a file-input
  fallback (`accept="image/*" capture="user"`); submits a stub POST to
  `/api/v1/members/{id}/alive` — unreachable endpoints are queued in
  localStorage and reported as "scheduled".
- **Locale switcher** EN ↔ PCM (Nigerian Pidgin), mirroring
  `internal/i18n` keys with never-panic fallback (`src/i18n.ts`).
- **Per-tenant theming** (PLT-14): CSS custom properties applied from
  `public/theme.json` (`default` + per-tenant overrides), warm
  low-saturation default palette.

## PWA behaviour

- `public/manifest.webmanifest`: name, short_name, maskable 192/512 icons,
  `theme_color`, `display: standalone`, `start_url: /`.
- `public/sw.js` (registered in `index.html`):
  - static assets: cache-first (precached shell + runtime caching),
  - `/api/*` GETs: network-first; successful responses (e.g. the
    last-known balance) are stored in CacheStorage and served offline,
    tagged with `X-Pwa-Cache: hit`,
  - navigation fallback: cached shell, then `offline.html`.
- `public/offline.html`: offline fallback page (EN + Pidgin copy).

## Develop

```bash
npm install
npm run dev        # Vite dev server; /api proxies to localhost:8096 (member-svc)
```

Run the backend first (from the repo root):

```bash
go run ./cmd/member-svc   # PORT=8096, dev KYC mock providers
```

Onboard through the UI (tenant defaults to `demo-pfa`) or sign in with the
RSA PIN (`member_id`) returned by `POST /api/v1/members`.

## Build / preview

```bash
npm run build      # tsc -b && vite build -> dist/
npm run preview    # serve the production build locally
```

Service workers only register on `localhost` or HTTPS origins.

## Install (PWA)

- Android/Chrome: menu → "Add to Home screen" / install prompt.
- iOS/Safari: Share → "Add to Home Screen".
- Desktop Chrome/Edge: install icon in the address bar.
