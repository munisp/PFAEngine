import { useCallback, useEffect, useState } from 'react'
import type { Api, MemberCase } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

// Support: member self-service case management (Tranche 20) — raise a
// complaint or service request, track every case with SLA status, and rate
// the resolution (CSAT). Same capabilities as WhatsApp intents 13/14.
export default function Support({ api, session, locale }: Props) {
  const [cases, setCases] = useState<MemberCase[]>([])
  const [type, setType] = useState<'COMPLAINT' | 'SERVICE_REQUEST' | 'GENERAL'>('COMPLAINT')
  const [subject, setSubject] = useState('')
  const [description, setDescription] = useState('')
  const [msg, setMsg] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const refresh = useCallback(async (): Promise<void> => {
    try {
      const r = await api.myCases(session.memberId)
      setCases(r.data.cases ?? [])
    } catch { /* offline-first: keep stale list */ }
  }, [api, session.memberId])

  useEffect(() => { void refresh() }, [refresh])

  async function raise(): Promise<void> {
    if (!subject.trim()) return
    setBusy(true); setErr(''); setMsg('')
    try {
      const r = await api.raiseCase(session.memberId, type, subject.trim(), description.trim())
      setMsg(
        r.data.merged
          ? `${t(locale, 'support.merged')} ${r.data.case.id}`
          : `${t(locale, 'support.opened')} ${r.data.case.id}`,
      )
      setSubject(''); setDescription('')
      await refresh()
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function rate(caseId: string, score: number): Promise<void> {
    setErr('')
    try {
      await api.rateCase(caseId, score)
      await refresh()
    } catch (e) { setErr(String(e)) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'support.title')}</h2>

      <section className="card">
        <h3>{t(locale, 'support.raise')}</h3>
        <label>
          {t(locale, 'support.type')}
          <select value={type} onChange={(e) => setType(e.target.value as typeof type)}>
            <option value="COMPLAINT">{t(locale, 'support.type.complaint')}</option>
            <option value="SERVICE_REQUEST">{t(locale, 'support.type.service')}</option>
            <option value="GENERAL">{t(locale, 'support.type.general')}</option>
          </select>
        </label>
        <label>
          {t(locale, 'support.subject')}
          <input value={subject} onChange={(e) => setSubject(e.target.value)} />
        </label>
        <label>
          {t(locale, 'support.description')}
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
        </label>
        <button type="button" onClick={() => void raise()} disabled={busy || !subject.trim()}>
          {t(locale, 'support.submit')}
        </button>
        {msg && <p className="ok">{msg}</p>}
        {err && <p className="err">{err}</p>}
      </section>

      <section className="card">
        <h3>{t(locale, 'support.my_cases')}</h3>
        <ul className="lesson-list">
          {cases.map((c) => (
            <li key={c.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <div>
                  <b>{c.subject}</b>
                  <div style={{ fontSize: 12, opacity: 0.75 }}>
                    {c.id} · {c.type} · {new Date(c.created_at).toLocaleDateString()}
                  </div>
                </div>
                <span style={{
                  alignSelf: 'center', fontSize: 12, padding: '2px 8px', borderRadius: 10,
                  background: c.status === 'RESOLVED' || c.status === 'CLOSED' ? '#f0fdf4' : c.sla_breached ? '#fef2f2' : '#eef2ff',
                }}>
                  {c.status}{c.sla_breached && c.status !== 'RESOLVED' && c.status !== 'CLOSED' ? ' ⚠' : ''}
                </span>
              </div>
              {c.resolution && (
                <p style={{ fontSize: 13, margin: '4px 0' }}>
                  {c.resolution_code}: {c.resolution}
                </p>
              )}
              {(c.status === 'RESOLVED' || c.status === 'CLOSED') && !c.csat && (
                <div style={{ display: 'flex', gap: 4, alignItems: 'center', fontSize: 13 }}>
                  {t(locale, 'support.rate')}
                  {[1, 2, 3, 4, 5].map((s) => (
                    <button key={s} type="button" className="ghost" onClick={() => void rate(c.id, s)}>{s}★</button>
                  ))}
                </div>
              )}
              {c.csat ? <div style={{ fontSize: 12 }}>{t(locale, 'support.rated')} {c.csat}/5</div> : null}
            </li>
          ))}
          {cases.length === 0 && <li>{t(locale, 'support.empty')}</li>}
        </ul>
      </section>
    </main>
  )
}
