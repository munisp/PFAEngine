import { useEffect, useState } from 'react'
import type { Api, FundPerf } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

/** fmtPct renders basis points as a signed percentage (1030 → "+10.30%"),
    with an em-dash for absent values. */
function fmtPct(bps?: number): { text: string; color: string } {
  if (bps === undefined || bps === null) return { text: '—', color: 'var(--muted)' }
  const pct = bps / 100
  return {
    text: `${pct >= 0 ? '+' : ''}${pct.toFixed(2)}%`,
    color: pct > 0 ? '#3f6b4f' : pct < 0 ? '#a8503c' : 'var(--muted)',
  }
}

function fmtPrice(kobo: number): string {
  return `₦${(kobo / 100).toLocaleString('en-NG', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

const RETURN_COLS: { key: keyof FundPerf; label: string }[] = [
  { key: 'return_3m_bps', label: '3M' },
  { key: 'return_6m_bps', label: '6M' },
  { key: 'return_12m_bps', label: '12M' },
  { key: 'return_36m_bps', label: '36M' },
  { key: 'ytd_bps', label: 'YTD' },
]

// Fund performance (Tranche 21): unit prices and trailing returns for every
// fund, straight from GET /funds/performance.
export default function Performance({ api, locale }: Props) {
  const [perf, setPerf] = useState<FundPerf[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  useEffect(() => {
    void (async () => {
      setLoading(true); setErr('')
      try {
        const r = await api.fundPerformance()
        setPerf(Object.values(r.data.performance ?? {}))
      } catch (e) { setErr(String(e)) } finally { setLoading(false) }
    })()
  }, [api])

  return (
    <main className="view">
      <h2>{t(locale, 'performance.title')}</h2>

      {loading && <p className="muted">{t(locale, 'common.loading')}</p>}
      {err && <p className="err">{err}</p>}
      {!loading && perf.length === 0 && !err && <p className="muted">{t(locale, 'performance.empty')}</p>}

      {perf.map((p) => (
        <section className="card" key={p.fund_id}>
          <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'baseline' }}>
            <h3>Fund {p.fund_id}</h3>
            <span className="muted" style={{ fontSize: 12 }}>
              {t(locale, 'performance.as_of')} {new Date(p.as_of).toLocaleDateString()}
            </span>
          </div>
          <div>
            <div className="muted" style={{ fontSize: 12 }}>{t(locale, 'performance.unit_price')}</div>
            <div style={{ fontSize: '1.6rem', fontWeight: 700, letterSpacing: '-0.02em' }}>
              {fmtPrice(p.unit_price_kobo)}
            </div>
          </div>
          <div>
            <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>{t(locale, 'performance.returns')}</div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(5, 1fr)', gap: 6, textAlign: 'center' }}>
              {RETURN_COLS.map((c) => {
                const r = fmtPct(p[c.key] as number | undefined)
                return (
                  <div key={c.label}>
                    <div className="muted" style={{ fontSize: 11 }}>{c.label}</div>
                    <div style={{ fontSize: 13, fontWeight: 650, color: r.color }}>{r.text}</div>
                  </div>
                )
              })}
            </div>
          </div>
        </section>
      ))}
    </main>
  )
}
