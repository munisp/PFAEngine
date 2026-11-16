import { useEffect, useState } from 'react'
import type { Api, MortgageApplication } from '../api'
import { formatNaira } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

// Home: PRA s.89(2) — up to 25% of RSA balance as mortgage equity.
export default function Home({ api, session, locale }: Props) {
  const [apps, setApps] = useState<MortgageApplication[]>([])
  const [balance, setBalance] = useState('')
  const [amount, setAmount] = useState('')
  const [lender, setLender] = useState('')
  const [employed, setEmployed] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  function refresh(): void {
    api.mortgageList(session.memberId)
      .then((r) => setApps(r.data.applications ?? [])).catch(() => {})
  }
  useEffect(refresh, [api, session.memberId])

  const maxEquity = Math.floor(Number(balance || 0) / 4)

  async function apply(): Promise<void> {
    setBusy(true); setErr('')
    try {
      await api.mortgageApply({
        member_id: session.memberId,
        rsa_balance_kobo: Math.round(Number(balance) * 100),
        amount_kobo: Math.round(Number(amount) * 100),
        lender_ref: lender,
        employed,
      })
      setAmount(''); setLender('')
      refresh()
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'home.title')}</h2>
      <section className="card">
        <p>{t(locale, 'home.blurb')}</p>
        <label>{t(locale, 'home.balance')}
          <input inputMode="numeric" value={balance} onChange={(e) => setBalance(e.target.value)} />
        </label>
        {balance && <p className="muted">{t(locale, 'home.max_equity')}: {formatNaira(maxEquity * 100)}</p>}
        <label>{t(locale, 'home.amount')}
          <input inputMode="numeric" value={amount} onChange={(e) => setAmount(e.target.value)} />
        </label>
        <label>{t(locale, 'home.lender')}
          <input value={lender} onChange={(e) => setLender(e.target.value)} />
        </label>
        <label className="row">
          <input type="checkbox" checked={employed} onChange={(e) => setEmployed(e.target.checked)} />
          {t(locale, 'home.employed')}
        </label>
        <button type="button" disabled={busy} onClick={() => void apply()}>{t(locale, 'home.apply_cta')}</button>
      </section>
      {apps.length > 0 && (
        <section className="card">
          <h3>{t(locale, 'home.applications')}</h3>
          <ul>
            {apps.map((a) => (
              <li key={a.id}>
                {formatNaira(a.amount_kobo)} · <strong>{a.status}</strong>
                {a.reason ? ` — ${a.reason}` : ''}
              </li>
            ))}
          </ul>
        </section>
      )}
      {err && <p className="error">{err}</p>}
    </main>
  )
}
