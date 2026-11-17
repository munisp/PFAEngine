# INTEGRATION — Analytics & Lakehouse

What the lead must wire to complete this tranche. All files below are new
except `deploy/docker-compose.yml` (appended only — see the
`# ---- Analytics lakehouse` section).

## 1. Staff portal route (REQUIRED — only remaining code wiring)

`apps/staff-portal/src/views/Analytics.tsx` exists but is not routed. In
`apps/staff-portal/src/App.tsx`:

```tsx
import Analytics from './views/Analytics';
// 1. add 'analytics' to the `view` union type
// 2. add a nav button:
<button onClick={() => setView('analytics')}>Analytics</button>
// 3. render:
{view === 'analytics' && <Analytics api={api} />}
```

The view is self-contained: it calls analytics-svc directly with a tiny
local `afetch()` helper and does NOT touch `api.ts`. Base URL resolution:
`import.meta.env.VITE_ANALYTICS_BASE` with fallback
`http://localhost:8097`. If you prefer consistency, fold these into the
`Api` class (then the view should use `api.*`):

```ts
analyticsOverview() { return this.req<any>('GET', '/v1/analytics/overview'); }
analyticsTimeseries(metric: string, from?: string, to?: string) { ... }
analyticsEmployerHealth() { ... }
analyticsFundAum() { ... }
```

Note: `api.req` prefixes `this.base` (api-gateway :8080). Either point the
gateway at analytics-svc routes or keep the separate base URL — the
current view takes the separate-base approach to avoid gateway changes.

When the lakehouse is not yet populated, the view shows an amber
"run analytics-run" notice (503 ANALYTICS_NOT_READY) — that is intended,
not a bug.

## 2. Compose services (already added)

- `analytics-svc` — Go service, port **8097**, `DATABASE_URL` pointed at
  the compose Postgres. Healthcheck on /healthz.
- `analytics-run` — one-shot python runner (profile `analytics`, so it
  does not start on plain `docker compose up`; run explicitly:
  `docker compose --profile analytics run --rm analytics-run`, or
  schedule via cron). Writes lake to volume `lakedata`, exports gold to
  Postgres schema `analytics`.
- `grafana` — port **3001** (3000 avoided), admin password via
  `GRAFANA_ADMIN_PASSWORD` (default `admin` — change for any real deploy).
  Auto-provisions datasource + 3 dashboards from `deploy/grafana/`.

Suggested bring-up:

```bash
docker compose -f deploy/docker-compose.yml up -d postgres analytics-svc grafana
docker compose -f deploy/docker-compose.yml --profile analytics run --rm analytics-run
open http://localhost:3001   # admin/admin → PFAEngine folder
```

## 3. Env vars

| Var | Where | Purpose |
|---|---|---|
| `DATABASE_URL` | analytics-svc, analytics-run | Postgres DSN (extract source + export target) |
| `LAKE_DIR` | analytics-run | lake root (default `/lake` in container, `./lake` locally) |
| `PORT` | analytics-svc | default 8097 |
| `GRAFANA_ADMIN_PASSWORD` | grafana | admin login |
| `POSTGRES_HOST/PORT/DB/USER/PASSWORD` | grafana | datasource provisioning (compose sets them) |
| `VITE_ANALYTICS_BASE` | staff-portal build | analytics-svc URL (default http://localhost:8097) |
| `TEST_DATABASE_URL` | dev only | enables the Postgres export round-trip test |

## 4. Deploy notes

- `deploy/analytics.Dockerfile` is new (python:3.12-slim + `analytics/` +
  `db/migrations/`); the generic `deploy/Dockerfile` builds
  `analytics-svc` via `--build-arg SERVICE=analytics-svc` unchanged.
- Grafana provisioning is read-only mounted; dashboards update on file
  change (`updateIntervalSeconds: 30`).
- No changes were made to `cmd/member-svc`, `api/openapi.yaml`, or any
  existing app files.

## 5. Docs

`docs/ANALYTICS.md` — full maturity assessment (pre/post), data-flow
diagram, refresh cadence options, remaining gaps.
