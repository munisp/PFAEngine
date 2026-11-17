import { useCallback, useEffect, useState } from 'react'
import type { Api, PfaForm } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

const CATEGORIES = [
  'RSA_REGISTRATION',
  'TRANSFER',
  'WITHDRAWAL',
  'KYC',
  'NOMINATION',
  'MORTGAGE',
  'MICRO_PENSION',
  'GUIDE',
] as const

// Forms & downloads (Tranche 21): category-filtered catalogue of the PFA's
// published forms with direct download links.
export default function Forms({ api, locale }: Props) {
  const [cat, setCat] = useState('')
  const [forms, setForms] = useState<PfaForm[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  const refresh = useCallback(async (): Promise<void> => {
    setLoading(true); setErr('')
    try {
      const r = await api.formsList(cat || undefined)
      setForms(r.data.forms ?? [])
    } catch (e) { setErr(String(e)) } finally { setLoading(false) }
  }, [api, cat])

  useEffect(() => { void refresh() }, [refresh])

  return (
    <main className="view">
      <h2>{t(locale, 'forms.title')}</h2>

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
            {c ? t(locale, `forms.cat.${c}`) : t(locale, 'forms.all')}
          </button>
        ))}
      </div>

      <section className="card">
        {loading && <p className="muted">{t(locale, 'common.loading')}</p>}
        {err && <p className="err">{err}</p>}
        <ul className="lesson-list">
          {!loading && forms.map((f) => (
            <li key={f.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <div>
                  <b>{f.title}</b>
                  <div style={{ fontSize: 12, opacity: 0.75 }}>
                    {f.code} · {t(locale, 'forms.version')} {f.version}
                  </div>
                  {f.description && <p style={{ fontSize: 13, margin: '4px 0' }}>{f.description}</p>}
                </div>
                <a
                  href={f.ref}
                  target="_blank"
                  rel="noreferrer"
                  style={{ alignSelf: 'center', color: 'var(--accent)', fontWeight: 600, fontSize: 13, whiteSpace: 'nowrap' }}
                >
                  {t(locale, 'forms.download')}
                </a>
              </div>
            </li>
          ))}
          {!loading && forms.length === 0 && !err && <li>{t(locale, 'forms.empty')}</li>}
        </ul>
      </section>
    </main>
  )
}
