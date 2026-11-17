import { useCallback, useEffect, useState } from 'react';
import { Api, naira } from '../api';

// Analytics & lakehouse view — read-only dashboards over the lakehouse gold
// models served by analytics-svc (:8097). Deliberately self-contained: it
// does NOT extend api.ts (see INTEGRATION-analytics.md — the lead may fold
// these calls into the Api class later). Charts are hand-rolled SVG, same
// approach as Funds.tsx (no chart library in package.json).
//
// Fail-closed contract: when the lakehouse hasn't been populated yet the
// service answers 503 ANALYTICS_NOT_READY and this view shows setup
// guidance instead of invented numbers.

const ANALYTICS_BASE: string =
  (import.meta as any).env?.VITE_ANALYTICS_BASE ?? 'http://localhost:8097';

interface FunnelStage { stage: string; sessions: number }
interface Overview {
  total_members: number; total_active_members: number; members_as_of: string;
  aum_kobo: number; aum_as_of: string;
  last_contribution_month: string; last_month_gross_kobo: number; prev_month_gross_kobo: number;
  case_sla_compliance_bps: number; case_sla_month: string;
  kyc_funnel: FunnelStage[];
}
interface TSPoint { label: string; value: number; value2?: number }
interface Timeseries { metric: string; points: TSPoint[] }
interface EmployerHealth {
  employer_id: string; employer_name: string; compliance_state: string;
  schedules_total: number; schedules_posted: number; months_remitted: number;
  total_remitted_kobo: number; last_contribution_date?: string;
  remittance_health: 'HEALTHY' | 'WATCH' | 'DEFAULTING';
}
interface FundAUM {
  fund_id: string; fund_name: string; as_of: string;
  price_kobo: number; units_raw: number; aum_kobo: number;
}

class NotReadyError extends Error {}

async function afetch<T>(tenantId: string, path: string): Promise<T> {
  const res = await fetch(ANALYTICS_BASE + path, {
    headers: { 'X-Tenant-ID': tenantId },
  });
  if (res.status === 503) throw new NotReadyError('ANALYTICS_NOT_READY');
  if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
  return res.json();
}

export default function Analytics({ api }: { api: Api }) {
  const [overview, setOverview] = useState<Overview>();
  const [growth, setGrowth] = useState<TSPoint[]>([]);
  const [contrib, setContrib] = useState<TSPoint[]>([]);
  const [employers, setEmployers] = useState<EmployerHealth[]>([]);
  const [funds, setFunds] = useState<FundAUM[]>([]);
  const [notReady, setNotReady] = useState(false);
  const [err, setErr] = useState('');

  const load = useCallback(async () => {
    const t = api.tenantId;
    try {
      setOverview(await afetch<Overview>(t, '/v1/analytics/overview'));
      setNotReady(false); setErr('');
    } catch (e: any) {
      if (e instanceof NotReadyError) { setNotReady(true); return; }
      setErr(e.message); return;
    }
    const safe = <T,>(p: Promise<T>, apply: (v: T) => void) =>
      p.then(apply).catch(() => {}); // partial panels never blank the page
    safe(afetch<Timeseries>(t, '/v1/analytics/timeseries?metric=member_growth'), r => setGrowth(r.points));
    safe(afetch<Timeseries>(t, '/v1/analytics/timeseries?metric=contribution_monthly'), r => setContrib(r.points));
    safe(afetch<{ employers: EmployerHealth[] }>(t, '/v1/analytics/employers/health'), r => setEmployers(r.employers));
    safe(afetch<{ funds: FundAUM[] }>(t, '/v1/analytics/funds/aum'), r => setFunds(r.funds));
  }, [api]);

  useEffect(() => { load(); }, [load]);

  if (notReady) {
    return (
      <section>
        <h2>Analytics &amp; lakehouse</h2>
        <p style={{ background: '#fffbeb', padding: 12, borderRadius: 6 }}>
          The analytics schema is not populated yet (503 ANALYTICS_NOT_READY).
          Run the lakehouse pipeline first:&nbsp;
          <code>docker compose -f deploy/docker-compose.yml run analytics-run</code>
          &nbsp;(or <code>python3 analytics/run.py --dsn … --lake ./lake</code>),
          then refresh. See docs/ANALYTICS.md.
        </p>
        <button onClick={load}>Retry</button>
      </section>
    );
  }

  return (
    <section>
      <h2>Analytics &amp; lakehouse</h2>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      {!overview && !err && <p style={{ color: '#6b7280' }}>Loading…</p>}

      {overview && (
        <>
          <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 16 }}>
            <Stat label="Total AUM" value={naira(overview.aum_kobo)} sub={`as of ${overview.aum_as_of || '—'}`} />
            <Stat label="Members" value={String(overview.total_members)}
              sub={`${overview.total_active_members} active · as of ${overview.members_as_of || '—'}`} />
            <Stat label="Contributions (last month)" value={naira(overview.last_month_gross_kobo)}
              sub={`${overview.last_contribution_month || '—'} · prior ${naira(overview.prev_month_gross_kobo)}`} />
            <Stat label="Case SLA compliance" value={pct(overview.case_sla_compliance_bps)}
              sub={`month ${overview.case_sla_month || '—'}`} />
          </div>

          <h3>Member growth (cumulative)</h3>
          {growth.length > 1
            ? <Line points={growth.map(p => ({ x: p.label, y: p.value2 ?? 0 }))} />
            : <p style={{ color: '#6b7280' }}>No growth series yet.</p>}

          <h3>Contribution velocity (monthly gross)</h3>
          {contrib.length > 0
            ? <Bars points={contrib.map(p => ({ x: p.label.slice(0, 7), y: p.value }))} money />
            : <p style={{ color: '#6b7280' }}>No contribution data.</p>}

          <h3>KYC funnel</h3>
          <Funnel stages={overview.kyc_funnel} />
        </>
      )}

      <h3>Fund AUM</h3>
      {funds.length > 0 ? (
        <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
          <thead><tr><Th>Fund</Th><Th>As of</Th><Th>NAV</Th><Th>Units</Th><Th>AUM</Th></tr></thead>
          <tbody>
            {funds.map(f => (
              <tr key={f.fund_id}>
                <Td>{f.fund_name}</Td><Td>{f.as_of}</Td>
                <Td>{naira(f.price_kobo)}</Td>
                <Td>{(f.units_raw / 1e8).toLocaleString('en-NG', { maximumFractionDigits: 4 })}</Td>
                <Td>{naira(f.aum_kobo)}</Td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : <p style={{ color: '#6b7280' }}>No fund AUM data.</p>}

      <h3>Employer remittance health</h3>
      {employers.length > 0 ? (
        <table style={{ borderCollapse: 'collapse', width: '100%' }}>
          <thead><tr>
            <Th>Employer</Th><Th>Compliance</Th><Th>Schedules posted</Th>
            <Th>Months remitted</Th><Th>Total remitted</Th><Th>Health</Th>
          </tr></thead>
          <tbody>
            {employers.map(e => (
              <tr key={e.employer_id}>
                <Td>{e.employer_name}</Td>
                <Td>{e.compliance_state}</Td>
                <Td>{e.schedules_posted}/{e.schedules_total}</Td>
                <Td>{e.months_remitted}</Td>
                <Td>{naira(e.total_remitted_kobo)}</Td>
                <Td><HealthBadge health={e.remittance_health} /></Td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : <p style={{ color: '#6b7280' }}>No employer data.</p>}
    </section>
  );
}

const pct = (bps: number) => `${(bps / 100).toFixed(1)}%`;

function Stat({ label, value, sub }: { label: string; value: string; sub: string }) {
  return (
    <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: 12, minWidth: 180, background: '#fafafa' }}>
      <div style={{ fontSize: 12, color: '#6b7280' }}>{label}</div>
      <div style={{ fontSize: 22, fontWeight: 600 }}>{value}</div>
      <div style={{ fontSize: 11, color: '#9ca3af' }}>{sub}</div>
    </div>
  );
}

// Hand-rolled SVG line chart (same approach as Funds.tsx Sparkline).
function Line({ points }: { points: { x: string; y: number }[] }) {
  const W = 640, H = 160, PAD = 10;
  const ys = points.map(p => p.y);
  const min = Math.min(...ys), max = Math.max(...ys);
  const span = max - min || 1;
  const step = points.length > 1 ? (W - 2 * PAD) / (points.length - 1) : 0;
  const coords = points.map((p, i) =>
    `${(PAD + i * step).toFixed(1)},${(H - PAD - ((p.y - min) / span) * (H - 2 * PAD)).toFixed(1)}`).join(' ');
  return (
    <svg width={W} height={H} style={{ border: '1px solid #e5e7eb', borderRadius: 8, background: '#fafafa' }}
      role="img" aria-label="Member growth">
      <polyline points={coords} fill="none" stroke="#059669" strokeWidth={2} />
      <text x={PAD} y={14} fontSize={10} fill="#6b7280">peak {max.toLocaleString()}</text>
      <text x={PAD} y={H - 4} fontSize={10} fill="#6b7280">{points[0].x} → {points[points.length - 1].x}</text>
    </svg>
  );
}

// Hand-rolled SVG bar chart for monthly contribution velocity.
function Bars({ points, money }: { points: { x: string; y: number }[]; money?: boolean }) {
  const W = 640, H = 160, PAD = 10;
  const max = Math.max(...points.map(p => p.y), 1);
  const bw = Math.max(4, (W - 2 * PAD) / points.length - 4);
  return (
    <svg width={W} height={H} style={{ border: '1px solid #e5e7eb', borderRadius: 8, background: '#fafafa' }}
      role="img" aria-label="Contribution velocity">
      {points.map((p, i) => {
        const h = (p.y / max) * (H - 2 * PAD - 14);
        const x = PAD + i * ((W - 2 * PAD) / points.length);
        return (
          <g key={p.x}>
            <rect x={x} y={H - PAD - h} width={bw} height={h} fill="#4f46e5" rx={2}>
              <title>{p.x}: {money ? naira(p.y) : p.y}</title>
            </rect>
            {points.length <= 14 && (
              <text x={x} y={H - 1} fontSize={8} fill="#6b7280">{p.x}</text>
            )}
          </g>
        );
      })}
    </svg>
  );
}

function Funnel({ stages }: { stages: FunnelStage[] }) {
  if (!stages.length) return <p style={{ color: '#6b7280' }}>No KYC sessions yet.</p>;
  const max = Math.max(...stages.map(s => s.sessions), 1);
  return (
    <div style={{ marginBottom: 16 }}>
      {stages.map(s => (
        <div key={s.stage} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
          <span style={{ width: 150, fontSize: 12, textAlign: 'right' }}>{s.stage}</span>
          <div style={{ background: '#0ea5e9', height: 16, width: `${(s.sessions / max) * 60}%`, borderRadius: 3, minWidth: 2 }} />
          <span style={{ fontSize: 12 }}>{s.sessions.toLocaleString()}</span>
        </div>
      ))}
    </div>
  );
}

function HealthBadge({ health }: { health: EmployerHealth['remittance_health'] }) {
  const color = health === 'HEALTHY' ? '#059669' : health === 'WATCH' ? '#d97706' : '#dc2626';
  return <span style={{ color, fontWeight: 600 }}>{health}</span>;
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children }: { children: React.ReactNode }) =>
  <td style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
