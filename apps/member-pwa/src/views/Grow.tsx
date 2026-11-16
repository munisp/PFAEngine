import { useEffect, useState } from 'react'
import type { Api, AdvisoryProjection, EscalationRule, RoundupWallet } from '../api'
import { formatNaira } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

// Grow: retirement projection (advisory) + auto-escalation rule + round-ups.
export default function Grow({ api, session, locale }: Props) {
  const [projection, setProjection] = useState<AdvisoryProjection | null>(null)
  const [rule, setRule] = useState<EscalationRule | null>(null)
  const [wallet, setWallet] = useState<RoundupWallet | null>(null)
  const [balance, setBalance] = useState('')
  const [monthly, setMonthly] = useState('')
  const [years, setYears] = useState('20')
  const [salary, setSalary] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api.escalationRules(session.memberId)
      .then((r) => setRule(r.data.rules[0] ?? null)).catch(() => {})
    api.roundupWallet(session.memberId)
      .then((r) => setWallet(r.data.wallet)).catch(() => {})
  }, [api, session.memberId])

  async function project(): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.advisoryProject({
        balance_kobo: Math.round(Number(balance) * 100),
        monthly_contrib_kobo: Math.round(Number(monthly) * 100),
        years_to_retire: Number(years),
        annual_return_bps: 800, // 8% default long-run assumption
        salary_kobo: Math.round(Number(salary) * 100),
        replacement_bps: 5000,  // target 50% income replacement
      })
      setProjection(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function enrolEscalation(): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.escalationEnrol({
        member_id: session.memberId, base_rate_bps: 1800, step_bps: 100, max_bps: 2500,
      })
      setRule(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function optOut(): Promise<void> {
    setBusy(true); setErr('')
    try {
      await api.escalationOptOut({ member_id: session.memberId, reason: 'member request' })
      setRule(rule ? { ...rule, opted_out: true } : null)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function openRoundup(): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.roundupOpen({
        member_id: session.memberId, step_kobo: 10000, monthly_cap_kobo: 500000,
      })
      setWallet(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'grow.title')}</h2>

      <section className="card">
        <h3>{t(locale, 'grow.projection')}</h3>
        <label>{t(locale, 'grow.balance')}
          <input inputMode="numeric" value={balance} onChange={(e) => setBalance(e.target.value)} />
        </label>
        <label>{t(locale, 'grow.monthly')}
          <input inputMode="numeric" value={monthly} onChange={(e) => setMonthly(e.target.value)} />
        </label>
        <label>{t(locale, 'grow.years')}
          <input inputMode="numeric" value={years} onChange={(e) => setYears(e.target.value)} />
        </label>
        <label>{t(locale, 'grow.salary')}
          <input inputMode="numeric" value={salary} onChange={(e) => setSalary(e.target.value)} />
        </label>
        <button type="button" disabled={busy} onClick={() => void project()}>
          {t(locale, 'grow.project_cta')}
        </button>
        {projection && (
          <div className="result">
            <p><strong>{formatNaira(projection.projected_balance_kobo)}</strong> {t(locale, 'grow.projected')}</p>
            <p>{t(locale, 'grow.expected_pension')}: {formatNaira(projection.expected_pension_kobo)}/mo</p>
            <p>{t(locale, 'grow.target_pension')}: {formatNaira(projection.target_pension_kobo)}/mo</p>
            {projection.on_track
              ? <p className="ok">{t(locale, 'grow.on_track')}</p>
              : <p className="warn">{t(locale, 'grow.gap')} {formatNaira(projection.gap_kobo)}/mo — {t(locale, 'grow.extra')} {formatNaira(projection.extra_monthly_kobo)}/mo</p>}
          </div>
        )}
      </section>

      <section className="card">
        <h3>{t(locale, 'grow.escalation')}</h3>
        {rule ? (
          <>
            <p>{t(locale, 'grow.escalation_now')}: {rule.current_bps / 100}% → {t(locale, 'grow.escalation_cap')} {rule.max_bps / 100}%</p>
            {rule.opted_out
              ? <p className="warn">{t(locale, 'grow.opted_out')}</p>
              : <button type="button" className="ghost" disabled={busy} onClick={() => void optOut()}>{t(locale, 'grow.optout_cta')}</button>}
          </>
        ) : (
          <>
            <p>{t(locale, 'grow.escalation_blurb')}</p>
            <button type="button" disabled={busy} onClick={() => void enrolEscalation()}>{t(locale, 'grow.escalation_cta')}</button>
          </>
        )}
      </section>

      <section className="card">
        <h3>{t(locale, 'grow.roundup')}</h3>
        {wallet ? (
          <p>{t(locale, 'grow.roundup_now')}: {formatNaira(wallet.step_kobo)} · {t(locale, 'grow.roundup_month')}: {formatNaira(wallet.swept_this_month_kobo)}</p>
        ) : (
          <>
            <p>{t(locale, 'grow.roundup_blurb')}</p>
            <button type="button" disabled={busy} onClick={() => void openRoundup()}>{t(locale, 'grow.roundup_cta')}</button>
          </>
        )}
      </section>

      {err && <p className="error">{err}</p>}
    </main>
  )
}
