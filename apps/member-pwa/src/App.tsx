import { useEffect, useMemo, useState } from 'react'
import { Api } from './api'
import type { Locale } from './i18n'
import { LOCALES, t } from './i18n'
import type { Session } from './session'
import { clearSession, loadSession, saveSession } from './session'
import { applyTheme, DEFAULT_THEME, loadTheme } from './theme'
import Alive from './views/Alive'
import Dashboard from './views/Dashboard'
import Login from './views/Login'
import Statement from './views/Statement'

type View = 'dashboard' | 'statement' | 'alive'

const LOCALE_KEY = 'pfa.locale'

function initialLocale(): Locale {
  return localStorage.getItem(LOCALE_KEY) === 'pcm' ? 'pcm' : 'en'
}

export default function App() {
  const [session, setSession] = useState<Session | null>(() => loadSession())
  const [locale, setLocale] = useState<Locale>(() => initialLocale())
  const [view, setView] = useState<View>('dashboard')

  // Default palette paints immediately; the tenant theme overrides on load.
  useEffect(() => {
    applyTheme(DEFAULT_THEME)
    void loadTheme(session?.tenantId ?? 'demo-pfa')
  }, [session?.tenantId])

  useEffect(() => {
    localStorage.setItem(LOCALE_KEY, locale)
  }, [locale])

  const api = useMemo(() => new Api('', session?.tenantId ?? 'demo-pfa'), [session?.tenantId])

  function onSession(next: Session): void {
    saveSession(next)
    setSession(next)
    setView('dashboard')
  }

  function signOut(): void {
    clearSession()
    setSession(null)
  }

  const navItems: { id: View; key: string }[] = [
    { id: 'dashboard', key: 'nav.dashboard' },
    { id: 'statement', key: 'nav.statement' },
    { id: 'alive', key: 'nav.alive' },
  ]

  return (
    <div className="shell">
      <header className="topbar">
        <div className="brand">
          <strong>{t(locale, 'app.title')}</strong>
          <span>{session ? session.tenantId : 'PFAEngine'}</span>
        </div>
        <div className="locale-switch" role="group" aria-label="Language">
          {LOCALES.map((l) => (
            <button
              key={l.id}
              type="button"
              className={l.id === locale ? 'active' : ''}
              onClick={() => setLocale(l.id)}
            >
              {l.label}
            </button>
          ))}
        </div>
      </header>
      {!session ? (
        <Login locale={locale} onSession={onSession} />
      ) : (
        <>
          {view === 'dashboard' && <Dashboard api={api} session={session} locale={locale} />}
          {view === 'statement' && <Statement api={api} session={session} locale={locale} />}
          {view === 'alive' && <Alive api={api} session={session} locale={locale} />}
          <button className="ghost" type="button" onClick={signOut}>
            {t(locale, 'session.sign_out')}
          </button>
        </>
      )}
      {session && (
        <footer className="navbar">
          <nav>
            {navItems.map((item) => (
              <button
                key={item.id}
                type="button"
                className={item.id === view ? 'active' : ''}
                onClick={() => setView(item.id)}
              >
                {t(locale, item.key)}
              </button>
            ))}
          </nav>
        </footer>
      )}
    </div>
  )
}
