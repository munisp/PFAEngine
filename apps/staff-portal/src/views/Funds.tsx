import { useCallback, useEffect, useState } from 'react';
import { Api, naira } from '../api';
import type { FundPerf, NavPoint } from '../api';

// Fund NAV & performance — daily NAV publication (₦ naira in → kobo out),
// performance summary across funds, and a NAV series sparkline per fund.
const FUNDS: { id: string; name: string }[] = [
  { id: 'FUND_I', name: 'Fund I — Retiree' },
  { id: 'FUND_II', name: 'Fund II — Conservative' },
  { id: 'FUND_III', name: 'Fund III — Balanced' },
  { id: 'FUND_IV', name: 'Fund IV — Growth' },
  { id: 'FUND_V', name: 'Fund V — Micro Pension' },
  { id: 'FUND_VI', name: 'Fund VI — Non-Interest' },
];

export default function Funds({ api }: { api: Api }) {
  const [perf, setPerf] = useState<Record<string, FundPerf>>({});
  const [pubFund, setPubFund] = useState('FUND_II');
  const [pubDate, setPubDate] = useState(new Date().toISOString().slice(0, 10));
  const [pubPrice, setPubPrice] = useState('');
  const [seriesFund, setSeriesFund] = useState('FUND_II');
  const [series, setSeries] = useState<NavPoint[]>([]);
  const [err, setErr] = useState('');
  const [toast, setToast] = useState('');

  const loadPerf = useCallback(async () => {
    try { setPerf((await api.fundPerformance()).performance); setErr(''); }
    catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { loadPerf(); }, [loadPerf]);

  useEffect(() => {
    api.fundSeries(seriesFund).then(r => setSeries(r.series)).catch((e: any) => { setSeries([]); setErr(e.message); });
  }, [api, seriesFund]);

  const publish = async () => {
    const nairaValue = Number(pubPrice);
    if (!pubDate || !Number.isFinite(nairaValue) || nairaValue <= 0) { setErr('Enter a valid date and positive price.'); return; }
    const kobo = Math.round(nairaValue * 100); // money must be integers
    try {
      await api.fundNavPublish(pubFund, pubDate, kobo);
      setToast(`Published NAV for ${pubFund} on ${pubDate}: ${naira(kobo)}`);
      setPubPrice('');
      setErr('');
      api.fundSeries(seriesFund).then(r => setSeries(r.series)).catch(() => {});
    } catch (e: any) { setErr(e.message); }
  };

  return (
    <section>
      <h2>Fund NAV &amp; performance</h2>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      {toast && <p style={{ background: '#f0fdf4', padding: 8, borderRadius: 6 }}>{toast}</p>}

      <h3>Publish NAV</h3>
      <div style={{ display: 'flex', gap: 6, marginBottom: 16, flexWrap: 'wrap' }}>
        <select value={pubFund} onChange={e => setPubFund(e.target.value)}>
          {FUNDS.map(f => <option key={f.id} value={f.id}>{f.name}</option>)}
        </select>
        <input type="date" value={pubDate} onChange={e => setPubDate(e.target.value)} />
        <input placeholder="Price (₦)" value={pubPrice} onChange={e => setPubPrice(e.target.value)} inputMode="decimal" />
        <button onClick={publish} disabled={!pubPrice}>Publish</button>
      </div>

      <h3>Performance</h3>
      <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
        <thead><tr>
          <Th>Fund</Th><Th>NAV</Th><Th>As of</Th><Th>MTD</Th><Th>QTD</Th><Th>YTD</Th><Th>1Y</Th><Th>Since inception</Th>
        </tr></thead>
        <tbody>
          {FUNDS.map(f => {
            const p = perf[f.id];
            return (
              <tr key={f.id}>
                <Td>{f.name}</Td>
                <Td>{p?.nav_kobo != null ? naira(p.nav_kobo) : '—'}</Td>
                <Td>{p?.as_of ? new Date(p.as_of).toLocaleDateString() : '—'}</Td>
                <Td>{pct(p?.mtd_bps)}</Td><Td>{pct(p?.qtd_bps)}</Td><Td>{pct(p?.ytd_bps)}</Td>
                <Td>{pct(p?.one_year_bps)}</Td><Td>{pct(p?.inception_bps)}</Td>
              </tr>
            );
          })}
        </tbody>
      </table>

      <h3>NAV series</h3>
      <div style={{ marginBottom: 8 }}>
        <select value={seriesFund} onChange={e => setSeriesFund(e.target.value)}>
          {FUNDS.map(f => <option key={f.id} value={f.id}>{f.name}</option>)}
        </select>
      </div>
      {series.length > 0 ? <Sparkline points={series} /> : <p style={{ color: '#6b7280' }}>No NAV series available.</p>}
      {series.length > 0 && (
        <table style={{ borderCollapse: 'collapse', marginTop: 8 }}>
          <thead><tr><Th>Date</Th><Th>NAV</Th></tr></thead>
          <tbody>
            {series.slice(-12).map(p => (
              <tr key={p.date}>
                <Td>{p.date}</Td><Td>{naira(p.price_kobo)}</Td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

const pct = (bps?: number) => bps == null ? '—' : `${(bps / 100).toFixed(2)}%`;

// Simple inline SVG sparkline (no chart library): min/max scaled polyline.
function Sparkline({ points }: { points: NavPoint[] }) {
  const W = 640, H = 120, PAD = 8;
  const prices = points.map(p => p.price_kobo);
  const min = Math.min(...prices), max = Math.max(...prices);
  const span = max - min || 1;
  const step = points.length > 1 ? (W - 2 * PAD) / (points.length - 1) : 0;
  const coords = points.map((p, i) => {
    const x = PAD + i * step;
    const y = H - PAD - ((p.price_kobo - min) / span) * (H - 2 * PAD);
    return `${x.toFixed(1)},${y.toFixed(1)}`;
  }).join(' ');
  return (
    <svg width={W} height={H} style={{ border: '1px solid #e5e7eb', borderRadius: 8, background: '#fafafa' }}
      role="img" aria-label="NAV sparkline">
      <polyline points={coords} fill="none" stroke="#4f46e5" strokeWidth={2} />
      <text x={PAD} y={14} fontSize={10} fill="#6b7280">high {naira(max)}</text>
      <text x={PAD} y={H - 4} fontSize={10} fill="#6b7280">low {naira(min)}</text>
    </svg>
  );
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children }: { children: React.ReactNode }) =>
  <td style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
