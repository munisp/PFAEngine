import { useCallback, useEffect, useState } from 'react'
import { formatNaira } from '../api'
import type { Api, BalanceResponse, StatementPayload } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props {
  api: Api;
  session: Session;
  locale: Locale;
}

interface CachedDashboard {
  balance: BalanceResponse;
  statement: StatementPayload | null;
}

const cacheKey = (memberId: string) => `pfa.dashboard.${memberId}`;

function saveCache(memberId: string, data: CachedDashboard): void {
  try {
    localStorage.setItem(cacheKey(memberId), JSON.stringify(data));
  } catch {
    // storage full / unavailable — non-fatal
  }
}

function loadCache(memberId: string): CachedDashboard | null {
  try {
    const raw = localStorage.getItem(cacheKey(memberId));
    return raw ? (JSON.parse(raw) as CachedDashboard) : null;
  } catch {
    return null;
  }
}

// Dashboard: balance card (naira formatting), fund code badge, last
// contribution, offline indicator when the service worker served the
// last-known balance from CacheStorage.
export default function Dashboard({ api, session, locale }: Props) {
  const [balance, setBalance] = useState<BalanceResponse | null>(null)
  const [statement, setStatement] = useState<StatementPayload | null>(null)
  const [fromCache, setFromCache] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const bal = await api.balance(session.memberId)
      setBalance(bal.data)
      let stmt: StatementPayload | null = null
      let cached = bal.fromCache
      try {
        const s = await api.statement(session.memberId)
        stmt = s.data
        cached = cached || s.fromCache
      } catch {
        stmt = null
      }
      setStatement(stmt)
      setFromCache(cached)
      saveCache(session.memberId, { balance: bal.data, statement: stmt })
    } catch {
      // Fully offline with no SW copy: fall back to the localStorage snapshot.
      const snap = loadCache(session.memberId)
      if (snap) {
        setBalance(snap.balance)
        setStatement(snap.statement)
        setFromCache(true)
      } else {
        setError(t(locale, 'error.load'))
      }
    } finally {
      setLoading(false)
    }
  }, [api, session.memberId, locale])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const lastContribution = (statement?.transactions ?? [])
    .filter((txn) => txn.amount_kobo > 0)
    .at(-1)
  const fundCode = statement?.holdings?.[0]?.fund_id ?? 'Fund II'

  return (
    <>
      {(fromCache || !navigator.onLine) && (
        <div className="banner">{t(locale, 'balance.offline')}</div>
      )}
      {error && <div className="banner">{error}</div>}
      <div className="card balance-card">
        <span className="badge">{t(locale, 'fund.badge')} · {fundCode}</span>
        <span className="label">{t(locale, 'balance')}</span>
        <span className="amount">
          {balance ? formatNaira(balance.balance_kobo) : '—'}
        </span>
        <span className="sub">{session.name || session.memberId}</span>
        <button className="ghost" type="button" onClick={() => void refresh()} disabled={loading}>
          {loading ? t(locale, 'common.loading') : t(locale, 'balance.refresh')}
        </button>
      </div>
      <div className="card">
        <h2>{t(locale, 'last_contribution')}</h2>
        {lastContribution ? (
          <>
            <p>{formatNaira(lastContribution.amount_kobo)}</p>
            <p className="muted">{lastContribution.date} · {lastContribution.description}</p>
          </>
        ) : (
          <p className="muted">{t(locale, 'last_contribution.none')}</p>
        )}
      </div>
    </>
  )
}
