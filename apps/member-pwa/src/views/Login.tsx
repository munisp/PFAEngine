import { useState } from 'react'
import { Api } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props {
  locale: Locale;
  onSession: (session: Session) => void;
}

// Login/onboard screen: sign in with RSA PIN + phone (member flow), or open
// a new RSA through the KYC-gated onboarding endpoint (POST /api/v1/members).
export default function Login({ locale, onSession }: Props) {
  const [mode, setMode] = useState<'login' | 'onboard'>('login')
  const [tenantId, setTenantId] = useState('demo-pfa')
  const [pin, setPin] = useState('')
  const [phone, setPhone] = useState('')
  const [fullName, setFullName] = useState('')
  const [dob, setDob] = useState('')
  const [email, setEmail] = useState('')
  const [nin, setNin] = useState('')
  const [bvn, setBvn] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function signIn(ev: React.FormEvent) {
    ev.preventDefault()
    setBusy(true)
    setError('')
    try {
      const api = new Api('', tenantId.trim())
      // The PIN is the member ID in this deployment; a successful balance
      // read confirms the RSA exists under this tenant.
      const { data } = await api.balance(pin.trim())
      onSession({ memberId: data.member_id, name: '', phone: phone.trim(), tenantId: tenantId.trim() })
    } catch {
      setError(t(locale, 'error.load'))
    } finally {
      setBusy(false)
    }
  }

  async function onboard(ev: React.FormEvent) {
    ev.preventDefault()
    setBusy(true)
    setError('')
    try {
      const api = new Api('', tenantId.trim())
      const { data } = await api.onboard({
        full_name: fullName.trim(),
        dob,
        phone: phone.trim(),
        email: email.trim(),
        nin: nin.trim(),
        ...(bvn.trim() ? { bvn: bvn.trim() } : {}),
      })
      onSession({ memberId: data.member_id, name: data.full_name, phone: phone.trim(), tenantId: tenantId.trim() })
    } catch {
      setError(t(locale, 'error.generic'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card">
      <h1>{mode === 'login' ? t(locale, 'login.title') : t(locale, 'onboard.title')}</h1>
      <p className="muted">{t(locale, 'app.tagline')}</p>
      {error && <div className="banner">{error}</div>}
      {mode === 'login' ? (
        <form onSubmit={signIn}>
          <label>
            {t(locale, 'login.tenant')}
            <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} required />
          </label>
          <label>
            {t(locale, 'login.rsa_pin')}
            <input value={pin} onChange={(e) => setPin(e.target.value)} autoComplete="username" required />
          </label>
          <label>
            {t(locale, 'login.phone')}
            <input value={phone} onChange={(e) => setPhone(e.target.value)} type="tel" inputMode="tel" required />
          </label>
          <button className="primary" type="submit" disabled={busy}>{t(locale, 'login.submit')}</button>
          <button className="ghost" type="button" onClick={() => setMode('onboard')}>
            {t(locale, 'login.onboard_cta')}
          </button>
        </form>
      ) : (
        <form onSubmit={onboard}>
          <label>
            {t(locale, 'login.tenant')}
            <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} required />
          </label>
          <label>
            {t(locale, 'onboard.full_name')}
            <input value={fullName} onChange={(e) => setFullName(e.target.value)} autoComplete="name" required />
          </label>
          <label>
            {t(locale, 'onboard.dob')}
            <input value={dob} onChange={(e) => setDob(e.target.value)} type="date" required />
          </label>
          <label>
            {t(locale, 'onboard.phone')}
            <input value={phone} onChange={(e) => setPhone(e.target.value)} type="tel" inputMode="tel" required />
          </label>
          <label>
            {t(locale, 'onboard.email')}
            <input value={email} onChange={(e) => setEmail(e.target.value)} type="email" autoComplete="email" />
          </label>
          <label>
            {t(locale, 'onboard.nin')}
            <input value={nin} onChange={(e) => setNin(e.target.value)} inputMode="numeric" pattern="\d{11}" required />
          </label>
          <label>
            {t(locale, 'onboard.bvn')}
            <input value={bvn} onChange={(e) => setBvn(e.target.value)} inputMode="numeric" />
          </label>
          <button className="primary" type="submit" disabled={busy}>{t(locale, 'onboard.submit')}</button>
          <button className="ghost" type="button" onClick={() => setMode('login')}>
            {t(locale, 'onboard.login_cta')}
          </button>
        </form>
      )}
    </div>
  )
}
