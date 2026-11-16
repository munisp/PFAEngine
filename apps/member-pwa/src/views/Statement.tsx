import { useEffect, useState } from 'react'
import { toDataURL } from 'qrcode'
import { formatNaira } from '../api'
import type { Api, StatementPayload } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props {
  api: Api;
  session: Session;
  locale: Locale;
}

// Statement screen: fetches the statement payload with its HMAC verify code
// (FR-501), renders the verification bundle as a QR, and re-checks the code
// via POST /api/v1/statements/verify. If QR rendering fails for any reason,
// the code is shown as text instead (graceful fallback).
export default function Statement({ api, session, locale }: Props) {
  const [stmt, setStmt] = useState<StatementPayload | null>(null)
  const [qrUrl, setQrUrl] = useState('')
  const [qrFailed, setQrFailed] = useState(false)
  const [verified, setVerified] = useState<boolean | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    setError('')
    api.statement(session.memberId)
      .then(async ({ data }) => {
        if (cancelled) return
        setStmt(data)
        try {
          const payload = JSON.stringify({
            rsa_id: data.rsa_id,
            period: data.period,
            closing_kobo: data.closing_kobo,
            code: data.verify_code,
          })
          const url = await toDataURL(payload, { margin: 1, width: 220 })
          if (!cancelled) setQrUrl(url)
        } catch {
          if (!cancelled) setQrFailed(true)
        }
      })
      .catch(() => { if (!cancelled) setError(t(locale, 'error.load')) })
    return () => { cancelled = true }
  }, [api, session.memberId, locale])

  async function verify() {
    if (!stmt) return
    setBusy(true)
    setError('')
    try {
      const { data } = await api.verifyStatement({
        rsa_id: stmt.rsa_id,
        period: stmt.period,
        closing_kobo: stmt.closing_kobo,
        code: stmt.verify_code,
      })
      setVerified(data.valid)
    } catch {
      setError(t(locale, 'error.generic'))
    } finally {
      setBusy(false)
    }
  }

  if (error && !stmt) return <div className="banner">{error}</div>
  if (!stmt) return <p className="muted">{t(locale, 'common.loading')}</p>

  return (
    <>
      <div className="card">
        <h1>{t(locale, 'statement')}</h1>
        <p className="muted">{stmt.member_name} · {stmt.rsa_pin}</p>
        <p><span className="muted">{t(locale, 'statement.period')}:</span> {stmt.period}</p>
        <p>
          <span className="muted">{t(locale, 'statement.closing')}:</span>{' '}
          <strong>{formatNaira(stmt.closing_kobo)}</strong>
        </p>
        <p className="muted">
          {t(locale, 'statement.generated')}: {new Date(stmt.generated_at).toLocaleString()}
        </p>
      </div>
      <div className="card">
        <h2>{t(locale, 'verify')}</h2>
        {qrUrl && !qrFailed
          ? <img className="qr" src={qrUrl} alt={t(locale, 'verify')} />
          : <p className="code-text">{stmt.verify_code}</p>}
        <p className="muted">{t(locale, 'statement.verify_hint')}</p>
        {verified === true && <div className="banner ok">{t(locale, 'verify.valid')}</div>}
        {verified === false && <div className="banner">{t(locale, 'verify.invalid')}</div>}
        {error && <div className="banner">{error}</div>}
        <button className="primary" type="button" onClick={() => void verify()} disabled={busy}>
          {t(locale, 'verify')}
        </button>
      </div>
    </>
  )
}
