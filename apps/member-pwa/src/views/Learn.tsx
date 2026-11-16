import { useEffect, useState } from 'react'
import type { Api, Lesson } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

// Learn: pension literacy centre — localized lessons with a quiz each.
export default function Learn({ api, session, locale }: Props) {
  const [lessons, setLessons] = useState<Lesson[]>([])
  const [open, setOpen] = useState<Lesson | null>(null)
  const [picked, setPicked] = useState<number | null>(null)
  const [result, setResult] = useState<boolean | null>(null)
  const [score, setScore] = useState<{ passed: number; total: number } | null>(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.literacyCatalogue(locale === 'pcm' ? 'pcm' : 'en')
      .then((r) => setLessons(r.data.lessons ?? [])).catch(() => {})
    api.literacyScore(session.memberId).then((r) => setScore(r.data)).catch(() => {})
  }, [api, session.memberId, locale])

  async function answer(): Promise<void> {
    if (open === null || picked === null) return
    setErr('')
    try {
      const r = await api.literacyAnswer({ member_id: session.memberId, lesson_id: open.id, option: picked })
      setResult(r.data.passed)
      const s = await api.literacyScore(session.memberId)
      setScore(s.data)
    } catch (e) { setErr(String(e)) }
  }

  return (
    <main className="view">
      <h2>{t(locale, 'learn.title')}</h2>
      {score && <p className="muted">{t(locale, 'learn.score')}: {score.passed}/{score.total}</p>}
      {!open ? (
        <section className="card">
          <ul className="lesson-list">
            {lessons.map((l) => (
              <li key={l.id}>
                <button type="button" className="ghost" onClick={() => { setOpen(l); setPicked(null); setResult(null) }}>
                  {l.title}
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : (
        <section className="card">
          <h3>{open.title}</h3>
          <p>{open.body}</p>
          <fieldset>
            <legend>{open.question}</legend>
            {open.options.map((o, i) => (
              <label key={i} className="row">
                <input type="radio" name="quiz" checked={picked === i} onChange={() => setPicked(i)} />
                {o}
              </label>
            ))}
          </fieldset>
          <div className="row">
            <button type="button" className="ghost" onClick={() => setOpen(null)}>{t(locale, 'learn.back')}</button>
            <button type="button" disabled={picked === null} onClick={() => void answer()}>{t(locale, 'learn.check')}</button>
          </div>
          {result !== null && (
            <p className={result ? 'ok' : 'warn'}>{t(locale, result ? 'learn.correct' : 'learn.wrong')}</p>
          )}
        </section>
      )}
      {err && <p className="error">{err}</p>}
    </main>
  )
}
