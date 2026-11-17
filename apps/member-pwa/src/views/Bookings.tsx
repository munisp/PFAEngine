import { useCallback, useEffect, useState } from 'react'
import type { Api, Appointment, Branch } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

function tomorrowISO(): string {
  const d = new Date()
  d.setDate(d.getDate() + 1)
  return d.toISOString().slice(0, 10)
}

/** slotLabel renders a slot index as a clock time when the branch's
    open_hours look like "HH:MM-HH:MM", else falls back to "Slot n". */
function slotLabel(branch: Branch, slot: number, fallback: string): string {
  const m = /^(\d{2}):(\d{2})/.exec(branch.open_hours)
  if (!m) return `${fallback} ${slot}`
  const mins = Number(m[1]) * 60 + Number(m[2]) + slot * branch.slot_minutes
  const hh = String(Math.floor(mins / 60) % 24).padStart(2, '0')
  const mm = String(mins % 60).padStart(2, '0')
  return `${hh}:${mm}`
}

function statusChipBg(status: string): string {
  if (status === 'DONE' || status === 'CHECKED_IN') return '#f0fdf4'
  if (status === 'CANCELLED' || status === 'NO_SHOW') return '#fef2f2'
  return '#eef2ff'
}

// Branch appointments (Tranche 21): pick a branch, a date and a free slot,
// book with a purpose, and track existing bookings.
export default function Bookings({ api, session, locale }: Props) {
  const [branches, setBranches] = useState<Branch[]>([])
  const [filter, setFilter] = useState('')
  const [branch, setBranch] = useState<Branch | null>(null)
  const [date, setDate] = useState('')
  const [slots, setSlots] = useState<number[] | null>(null)
  const [slot, setSlot] = useState<number | null>(null)
  const [purpose, setPurpose] = useState('')
  const [confirmation, setConfirmation] = useState<Appointment | null>(null)
  const [mine, setMine] = useState<Appointment[]>([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    void (async () => {
      try {
        const r = await api.branchesList()
        setBranches((r.data.branches ?? []).filter((b) => b.active))
      } catch { /* offline-first: keep stale list */ }
    })()
  }, [api])

  const refreshMine = useCallback(async (): Promise<void> => {
    try {
      const r = await api.myAppointments(session.memberId)
      setMine(r.data.appointments ?? [])
    } catch { /* offline-first: keep stale list */ }
  }, [api, session.memberId])

  useEffect(() => { void refreshMine() }, [refreshMine])

  useEffect(() => {
    if (!branch || !date) { setSlots(null); return }
    void (async () => {
      setSlots(null); setSlot(null); setErr('')
      try {
        const r = await api.branchAvailability(branch.id, date)
        setSlots(r.data.slots ?? [])
      } catch (e) { setErr(String(e)) }
    })()
  }, [api, branch, date])

  async function book(): Promise<void> {
    if (!branch || !date || slot === null || !purpose.trim()) return
    setBusy(true); setErr('')
    try {
      const r = await api.bookAppointment({
        branch_id: branch.id,
        member_id: session.memberId,
        date,
        slot,
        purpose: purpose.trim(),
      })
      setConfirmation(r.data)
      setBranch(null); setDate(''); setSlots(null); setSlot(null); setPurpose('')
      await refreshMine()
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  const visible = branches.filter(
    (b) => !filter.trim() || b.state.toLowerCase().includes(filter.trim().toLowerCase()),
  )

  return (
    <main className="view">
      <h2>{t(locale, 'bookings.title')}</h2>

      {confirmation && (
        <div className="banner ok">
          <b>{t(locale, 'bookings.confirmed')}</b>
          <p>
            {confirmation.id} · {confirmation.date} · {t(locale, 'bookings.slot')} {confirmation.slot} · {confirmation.purpose}
          </p>
        </div>
      )}

      <section className="card">
        <h3>{t(locale, 'bookings.pick_branch')}</h3>
        <label>
          {t(locale, 'bookings.filter_state')}
          <input value={filter} onChange={(e) => setFilter(e.target.value)} />
        </label>
        <ul className="lesson-list">
          {visible.map((b) => (
            <li key={b.id}>
              <button
                type="button"
                className="ghost"
                style={{ textAlign: 'left', width: '100%', fontWeight: branch?.id === b.id ? 700 : 600 }}
                onClick={() => { setBranch(b); setDate(''); setSlots(null); setSlot(null) }}
              >
                <b>{b.name}</b>
                <div style={{ fontSize: 12, opacity: 0.75 }}>
                  {b.address}, {b.city}, {b.state} · {t(locale, 'bookings.open_hours')} {b.open_hours}
                </div>
              </button>
            </li>
          ))}
          {visible.length === 0 && <li>{t(locale, 'bookings.no_branches')}</li>}
        </ul>
      </section>

      {branch && (
        <section className="card">
          <h3>{branch.name}</h3>
          <label>
            {t(locale, 'bookings.pick_date')}
            <input type="date" min={tomorrowISO()} value={date} onChange={(e) => setDate(e.target.value)} />
          </label>
          {date && slots !== null && (
            <>
              <p className="muted">{t(locale, 'bookings.pick_slot')}</p>
              {slots.length > 0 ? (
                <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: 6 }}>
                  {slots.map((s) => (
                    <button
                      key={s}
                      type="button"
                      className="secondary"
                      style={slot === s ? { borderColor: 'var(--accent)', background: 'var(--accent-soft)' } : undefined}
                      onClick={() => setSlot(s)}
                    >
                      {slotLabel(branch, s, t(locale, 'bookings.slot'))}
                    </button>
                  ))}
                </div>
              ) : (
                <p className="muted">{t(locale, 'bookings.no_slots')}</p>
              )}
            </>
          )}
          {slot !== null && (
            <label>
              {t(locale, 'bookings.purpose')}
              <input value={purpose} onChange={(e) => setPurpose(e.target.value)} />
            </label>
          )}
          <button
            className="primary"
            type="button"
            onClick={() => void book()}
            disabled={busy || slot === null || !purpose.trim()}
          >
            {t(locale, 'bookings.book')}
          </button>
          {err && <p className="err">{err}</p>}
        </section>
      )}

      <section className="card">
        <h3>{t(locale, 'bookings.mine')}</h3>
        <ul className="lesson-list">
          {mine.map((a) => (
            <li key={a.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <div>
                  <b>{a.date} · {t(locale, 'bookings.slot')} {a.slot}</b>
                  <div style={{ fontSize: 12, opacity: 0.75 }}>
                    {a.purpose} · {a.branch_id}
                  </div>
                </div>
                <span style={{
                  alignSelf: 'center', fontSize: 12, padding: '2px 8px', borderRadius: 10,
                  background: statusChipBg(a.status),
                }}>
                  {a.status}
                </span>
              </div>
            </li>
          ))}
          {mine.length === 0 && <li>{t(locale, 'bookings.empty')}</li>}
        </ul>
      </section>
    </main>
  )
}
