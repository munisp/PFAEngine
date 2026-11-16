import { useEffect, useState } from 'react'
import type { Api, GlidepathMember, GlidepathSwitch } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

const FUNDS = ['I', 'II', 'III', 'IV']

// Funds: PenCom multi-fund glidepath — current fund, election, switch history.
export default function Funds({ api, session, locale }: Props) {
  const [member, setMember] = useState<GlidepathMember | null>(null)
  const [switches, setSwitches] = useState<GlidepathSwitch[]>([])
  const [age, setAge] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    api.glidepathGet(session.memberId)
      .then((r) => { setMember(r.data.member); setSwitches(r.data.switches ?? []) })
      .catch(() => setMissing(true))
  }, [api, session.memberId])

  async function register(): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.glidepathRegister({
        member_id: session.memberId, age_years: Number(age), retired: false, aggressive: false,
      })
      setMember(r.data); setMissing(false)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function elect(fund: string): Promise<void> {
    setBusy(true); setErr('')
    try {
      await api.glidepathElect({ member_id: session.memberId, fund })
      const r = await api.glidepathGet(session.memberId)
      setMember(r.data.member); setSwitches(r.data.switches ?? [])
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'funds.title')}</h2>
      {missing && (
        <section className="card">
          <p>{t(locale, 'funds.register_blurb')}</p>
          <label>{t(locale, 'funds.age')}
            <input inputMode="numeric" value={age} onChange={(e) => setAge(e.target.value)} />
          </label>
          <button type="button" disabled={busy} onClick={() => void register()}>{t(locale, 'funds.register_cta')}</button>
        </section>
      )}
      {member && (
        <section className="card">
          <p>{t(locale, 'funds.current')}: <strong>Fund {member.fund}</strong> · {t(locale, 'funds.age_label')} {member.age_years}</p>
          <div className="fund-picker" role="group" aria-label="Fund election">
            {FUNDS.map((f) => (
              <button key={f} type="button" disabled={busy || f === member.fund}
                className={f === member.fund ? 'active' : ''}
                onClick={() => void elect(f)}>
                {t(locale, `funds.fund_${f}`)}
              </button>
            ))}
          </div>
          <p className="muted">{t(locale, 'funds.glidepath_note')}</p>
        </section>
      )}
      {switches.length > 0 && (
        <section className="card">
          <h3>{t(locale, 'funds.history')}</h3>
          <ul>
            {switches.map((s) => (
              <li key={s.id}>Fund {s.from} → Fund {s.to} · {new Date(s.at).toLocaleDateString()}</li>
            ))}
          </ul>
        </section>
      )}
      {err && <p className="error">{err}</p>}
    </main>
  )
}
