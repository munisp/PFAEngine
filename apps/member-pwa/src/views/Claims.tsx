import { useState } from 'react'
import type { Api, ClaimsPack } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

// Claims: fast-track retirement claim pack — checklist progress to
// straight-through processing (score 100).
export default function Claims({ api, session, locale }: Props) {
  const [pack, setPack] = useState<ClaimsPack | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  async function open(): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.claimsOpen(session.memberId)
      setPack(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function complete(item: string): Promise<void> {
    setBusy(true); setErr('')
    try {
      const r = await api.claimsComplete(session.memberId, item)
      setPack(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'claims.title')}</h2>
      {!pack ? (
        <section className="card">
          <p>{t(locale, 'claims.blurb')}</p>
          <button type="button" disabled={busy} onClick={() => void open()}>{t(locale, 'claims.open_cta')}</button>
        </section>
      ) : (
        <section className="card">
          <p><strong>{t(locale, 'claims.score')}: {pack.score}/100</strong></p>
          <div className="progress"><div style={{ width: `${pack.score}%` }} /></div>
          <ul>
            {pack.items.map((it) => (
              <li key={it.label} className="row">
                <input type="checkbox" checked={it.done} disabled={it.done || busy}
                  onChange={() => void complete(it.label)} />
                <span className={it.done ? 'done' : ''}>{it.label}</span>
              </li>
            ))}
          </ul>
          {pack.stp
            ? <p className="ok">{t(locale, 'claims.stp')}</p>
            : pack.missing.length > 0 && <p className="warn">{t(locale, 'claims.missing')}: {pack.missing.join(', ')}</p>}
        </section>
      )}
      {err && <p className="error">{err}</p>}
    </main>
  )
}
