import { useEffect, useState } from 'react'
import type { Api, Beneficiary, Nomination } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

const empty: Beneficiary = { name: '', relationship: '', phone: '', share_bps: 10000 }

// Family: versioned next-of-kin nominations (WORM history; shares must total 100%).
export default function Family({ api, session, locale }: Props) {
  const [nomination, setNomination] = useState<Nomination | null>(null)
  const [rows, setRows] = useState<Beneficiary[]>([{ ...empty }])
  const [err, setErr] = useState('')
  const [ok, setOk] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.successionGet(session.memberId).then((r) => setNomination(r.data)).catch(() => {})
  }, [api, session.memberId])

  const total = rows.reduce((s, r) => s + (r.share_bps || 0), 0)

  function setRow(i: number, patch: Partial<Beneficiary>): void {
    setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  }

  async function save(): Promise<void> {
    setBusy(true); setErr(''); setOk('')
    try {
      const r = await api.successionNominate({ member_id: session.memberId, beneficiaries: rows })
      setNomination(r.data)
      setOk(t(locale, 'family.saved'))
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'family.title')}</h2>
      {nomination && (
        <section className="card">
          <h3>{t(locale, 'family.current')} (v{nomination.version})</h3>
          <ul>
            {nomination.beneficiaries.map((b) => (
              <li key={b.name}>{b.name} ({b.relationship}) — {b.share_bps / 100}%</li>
            ))}
          </ul>
          <p className="muted">{t(locale, 'family.reconfirm')}: {new Date(nomination.next_reconfirm_due).toLocaleDateString()}</p>
        </section>
      )}
      <section className="card">
        <h3>{t(locale, 'family.new')}</h3>
        {rows.map((r, i) => (
          <div key={i} className="nominee-row">
            <input placeholder={t(locale, 'family.name')} value={r.name}
              onChange={(e) => setRow(i, { name: e.target.value })} />
            <input placeholder={t(locale, 'family.relationship')} value={r.relationship}
              onChange={(e) => setRow(i, { relationship: e.target.value })} />
            <input placeholder={t(locale, 'family.phone')} value={r.phone}
              onChange={(e) => setRow(i, { phone: e.target.value })} />
            <input inputMode="numeric" placeholder="%" value={r.share_bps / 100 || ''}
              onChange={(e) => setRow(i, { share_bps: Math.round(Number(e.target.value) * 100) })} />
          </div>
        ))}
        <p className={total === 10000 ? 'ok' : 'warn'}>{t(locale, 'family.total')}: {total / 100}%</p>
        <div className="row">
          <button type="button" className="ghost" onClick={() => setRows([...rows, { ...empty, share_bps: 0 }])}>
            {t(locale, 'family.add')}
          </button>
          <button type="button" disabled={busy || total !== 10000} onClick={() => void save()}>
            {t(locale, 'family.save')}
          </button>
        </div>
      </section>
      {ok && <p className="ok">{ok}</p>}
      {err && <p className="error">{err}</p>}
    </main>
  )
}
