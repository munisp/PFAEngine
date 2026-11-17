import { useCallback, useEffect, useState } from 'react'
import type { Api, Article, Disclosure } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

const CATEGORIES = ['PENCOM_NOTICE', 'INDUSTRY', 'PRODUCT', 'EDUCATION'] as const

// News & updates (Tranche 21): category-filtered feed (pinned first) plus the
// statutory disclosures library.
export default function News({ api, locale }: Props) {
  const [cat, setCat] = useState('')
  const [articles, setArticles] = useState<Article[]>([])
  const [disclosures, setDisclosures] = useState<Disclosure[]>([])
  const [expanded, setExpanded] = useState('')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  const refresh = useCallback(async (): Promise<void> => {
    setLoading(true); setErr('')
    try {
      const r = await api.newsFeed(cat || undefined)
      const list = r.data.articles ?? []
      setArticles([...list].sort((a, b) => Number(b.pinned) - Number(a.pinned)))
    } catch (e) { setErr(String(e)) } finally { setLoading(false) }
  }, [api, cat])

  useEffect(() => { void refresh() }, [refresh])

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.disclosuresList()
        setDisclosures(r.data.disclosures ?? [])
      } catch { /* offline-first: keep stale list */ }
    })()
  }, [api])

  return (
    <main className="view">
      <h2>{t(locale, 'news.title')}</h2>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {['', ...CATEGORIES].map((c) => (
          <button
            key={c || 'all'}
            type="button"
            className="ghost"
            style={{
              borderRadius: 999, padding: '4px 12px',
              background: cat === c ? 'var(--accent-soft)' : 'transparent',
              fontWeight: cat === c ? 700 : 600,
            }}
            onClick={() => setCat(c)}
          >
            {c ? t(locale, `news.cat.${c}`) : t(locale, 'news.all')}
          </button>
        ))}
      </div>

      <section className="card">
        {loading && <p className="muted">{t(locale, 'common.loading')}</p>}
        {err && <p className="err">{err}</p>}
        <ul className="lesson-list">
          {!loading && articles.map((a) => (
            <li key={a.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <b>{a.title}</b>
                {a.pinned && (
                  <span className="badge" style={{ alignSelf: 'flex-start' }}>
                    📌 {t(locale, 'news.pinned')}
                  </span>
                )}
              </div>
              <div style={{ fontSize: 12, opacity: 0.75 }}>
                {t(locale, `news.cat.${a.category}`)} · {new Date(a.published_at).toLocaleDateString()}
              </div>
              {expanded === a.id ? (
                <>
                  <p style={{ fontSize: 14, margin: '4px 0', whiteSpace: 'pre-wrap' }}>{a.body}</p>
                  <button type="button" className="ghost" style={{ alignSelf: 'flex-start' }} onClick={() => setExpanded('')}>
                    {t(locale, 'news.read_less')}
                  </button>
                </>
              ) : (
                <button type="button" className="ghost" style={{ alignSelf: 'flex-start' }} onClick={() => setExpanded(a.id)}>
                  {t(locale, 'news.read_more')}
                </button>
              )}
            </li>
          ))}
          {!loading && articles.length === 0 && !err && <li>{t(locale, 'news.empty')}</li>}
        </ul>
      </section>

      <section className="card">
        <h3>{t(locale, 'news.disclosures')}</h3>
        <ul className="lesson-list">
          {disclosures.map((d) => (
            <li key={d.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <div>
                  <b>{d.title}</b>
                  <div style={{ fontSize: 12, opacity: 0.75 }}>
                    {d.kind} · {d.period} · {new Date(d.published_at).toLocaleDateString()}
                  </div>
                </div>
                <a
                  href={d.ref}
                  target="_blank"
                  rel="noreferrer"
                  style={{ alignSelf: 'center', color: 'var(--accent)', fontWeight: 600, fontSize: 13, whiteSpace: 'nowrap' }}
                >
                  {t(locale, 'news.download')}
                </a>
              </div>
            </li>
          ))}
          {disclosures.length === 0 && <li>{t(locale, 'news.disclosures_empty')}</li>}
        </ul>
      </section>
    </main>
  )
}
