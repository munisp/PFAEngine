import { useEffect, useMemo, useState } from 'react'
import { Api } from './api'
import type { Locale } from './i18n'
import { LOCALES, t } from './i18n'
import type { Session } from './session'
import { clearSession, loadSession, saveSession } from './session'
import { applyTheme, DEFAULT_THEME, loadTheme } from './theme'
import Alive from './views/Alive'
import Bookings from './views/Bookings'
import Claims from './views/Claims'
import Dashboard from './views/Dashboard'
import Family from './views/Family'
import Forms from './views/Forms'
import Funds from './views/Funds'
import Grow from './views/Grow'
import Home from './views/Home'
import Kyc from './views/Kyc'
import Learn from './views/Learn'
import Login from './views/Login'
import News from './views/News'
import Performance from './views/Performance'
import Statement from './views/Statement'
import Support from './views/Support'

type View = 'dashboard' | 'grow' | 'funds' | 'family' | 'more'
  | 'statement' | 'alive' | 'claims' | 'home' | 'learn' | 'support'
  | 'kyc' | 'forms' | 'news' | 'bookings' | 'performance'

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
    { id: 'grow', key: 'nav.grow' },
    { id: 'funds', key: 'nav.funds' },
    { id: 'family', key: 'nav.family' },
    { id: 'more', key: 'nav.more' },
  ]

  const moreItems: { id: View; key: string }[] = [
    { id: 'kyc', key: 'nav.kyc' },
    { id: 'statement', key: 'nav.statement' },
    { id: 'alive', key: 'nav.alive' },
    { id: 'claims', key: 'nav.claims' },
    { id: 'home', key: 'nav.home' },
    { id: 'learn', key: 'nav.learn' },
    { id: 'forms', key: 'nav.forms' },
    { id: 'news', key: 'nav.news' },
    { id: 'bookings', key: 'nav.bookings' },
    { id: 'performance', key: 'nav.performance' },
    { id: 'support', key: 'nav.support' },
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
          {view === 'grow' && <Grow api={api} session={session} locale={locale} />}
          {view === 'funds' && <Funds api={api} session={session} locale={locale} />}
          {view === 'family' && <Family api={api} session={session} locale={locale} />}
          {view === 'statement' && <Statement api={api} session={session} locale={locale} />}
          {view === 'alive' && <Alive api={api} session={session} locale={locale} />}
          {view === 'claims' && <Claims api={api} session={session} locale={locale} />}
          {view === 'home' && <Home api={api} session={session} locale={locale} />}
          {view === 'learn' && <Learn api={api} session={session} locale={locale} />}
          {view === 'support' && <Support api={api} session={session} locale={locale} />}
          {view === 'kyc' && <Kyc api={api} session={session} locale={locale} />}
          {view === 'forms' && <Forms api={api} session={session} locale={locale} />}
          {view === 'news' && <News api={api} session={session} locale={locale} />}
          {view === 'bookings' && <Bookings api={api} session={session} locale={locale} />}
          {view === 'performance' && <Performance api={api} session={session} locale={locale} />}
          {view === 'more' && (
            <main className="view">
              <h2>{t(locale, 'nav.more')}</h2>
              <section className="card">
                <ul className="lesson-list">
                  {moreItems.map((m) => (
                    <li key={m.id}>
                      <button type="button" className="ghost" onClick={() => setView(m.id)}>
                        {t(locale, m.key)}
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            </main>
          )}
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
                className={item.id === view || (item.id === 'more' && moreItems.some((m) => m.id === view)) ? 'active' : ''}
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
