import { useCallback, useEffect, useState } from 'react'
import type { Api, DocKind, EkycSession, MemberDoc } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props { api: Api; session: Session; locale: Locale }

const DOC_KINDS: DocKind[] = [
  'PASSPORT_PHOTO',
  'NIN_SLIP',
  'ID_CARD',
  'PROOF_OF_ADDRESS',
  'BIRTH_CERTIFICATE',
  'EMPLOYMENT_LETTER',
]

const MAX_BYTES = 2 * 1024 * 1024

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

/** stripPrefix turns a data URL into raw base64 for the API body. */
function stripPrefix(dataUrl: string): string {
  const i = dataUrl.indexOf(',')
  return i >= 0 ? dataUrl.slice(i + 1) : dataUrl
}

async function sha256Hex(file: File): Promise<string> {
  try {
    const buf = await file.arrayBuffer()
    const digest = await crypto.subtle.digest('SHA-256', buf)
    return Array.from(new Uint8Array(digest)).map((b) => b.toString(16).padStart(2, '0')).join('')
  } catch {
    return '' // non-secure context: server still accepts ref-only submissions
  }
}

/** scorePct normalises a 0–1 or 0–100 confidence score to a whole percent. */
function scorePct(score: number): number {
  return Math.round(score <= 1 ? score * 100 : score)
}

function humaniseFlag(flag: string): string {
  return flag.replace(/_/g, ' ').toLowerCase()
}

function docChipBg(status: string): string {
  if (status === 'APPROVED') return '#f0fdf4'
  if (status === 'REJECTED') return '#fef2f2'
  return '#eef2ff'
}

// KYC Center (Tranche 21): the flagship eKYC stepper — document, identity,
// selfie (liveness + face match) and the final decision card — plus the
// member's general document wallet below.
export default function Kyc({ api, session, locale }: Props) {
  const [step, setStep] = useState<1 | 2 | 3 | 4>(1)
  const [ekyc, setEkyc] = useState<EkycSession | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  // Step 1 — document
  const [docKind, setDocKind] = useState<DocKind>('NIN_SLIP')
  const [docFile, setDocFile] = useState<File | null>(null)
  const [docDataUrl, setDocDataUrl] = useState('')
  const [tooLarge, setTooLarge] = useState(false)
  const [refOnly, setRefOnly] = useState(false)

  // Step 2 — identity
  const [nin, setNin] = useState('')
  const [bvn, setBvn] = useState('')
  const [name, setName] = useState(session.name)
  const [dob, setDob] = useState('')
  const [phone, setPhone] = useState(session.phone)

  // Step 3 — selfies
  const [frames, setFrames] = useState<string[]>([])

  // Step 4 — decision handled via ekyc.decision

  // My documents
  const [docs, setDocs] = useState<MemberDoc[]>([])
  const [upKind, setUpKind] = useState<DocKind>('ID_CARD')
  const [upFile, setUpFile] = useState<File | null>(null)
  const [docMsg, setDocMsg] = useState('')
  const [docErr, setDocErr] = useState('')
  const [docBusy, setDocBusy] = useState(false)

  const refreshDocs = useCallback(async (): Promise<void> => {
    try {
      const r = await api.myDocuments(session.memberId)
      setDocs(r.data.documents ?? [])
    } catch { /* offline-first: keep stale list */ }
  }, [api, session.memberId])

  useEffect(() => { void refreshDocs() }, [refreshDocs])

  async function ensureSession(): Promise<EkycSession> {
    if (ekyc) return ekyc
    const s = (await api.ekycStart(session.memberId)).data
    setEkyc(s)
    return s
  }

  async function refreshEkyc(id: string): Promise<EkycSession> {
    const fresh = (await api.ekycGet(id)).data
    setEkyc(fresh)
    return fresh
  }

  async function onDocFile(ev: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = ev.target.files?.[0]
    if (!file) return
    setDocFile(file)
    setTooLarge(file.size > MAX_BYTES)
    setRefOnly(false)
    setDocDataUrl(file.size > MAX_BYTES ? '' : await readAsDataURL(file))
  }

  async function submitDoc(): Promise<void> {
    if (!docFile) return
    setBusy(true); setErr('')
    try {
      const s = await ensureSession()
      const useImage = !tooLarge && !refOnly && !!docDataUrl
      await api.ekycSubmitDocument(s.id, {
        kind: docKind,
        ref: docFile.name,
        image_b64: useImage ? stripPrefix(docDataUrl) : '',
        use_vlm: useImage,
      })
      await refreshEkyc(s.id)
      setStep(2)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function submitIdentity(): Promise<void> {
    if (!ekyc || !/^\d{11}$/.test(nin)) return
    setBusy(true); setErr('')
    try {
      await api.ekycVerifyIdentity(ekyc.id, {
        nin,
        bvn: bvn.trim() || undefined,
        name: name.trim(),
        dob,
        phone: phone.trim(),
      })
      await refreshEkyc(ekyc.id)
      setStep(3)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  async function onSelfie(ev: React.ChangeEvent<HTMLInputElement>): Promise<void> {
    const file = ev.target.files?.[0]
    if (!file || frames.length >= 3) return
    const dataUrl = await readAsDataURL(file)
    setFrames([...frames, dataUrl])
    ev.target.value = ''
  }

  async function submitBiometric(): Promise<void> {
    if (!ekyc || frames.length === 0) return
    setBusy(true); setErr('')
    try {
      await api.ekycSubmitBiometric(ekyc.id, frames.map(stripPrefix))
      await refreshEkyc(ekyc.id)
      setStep(4)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }

  const decide = useCallback(async (): Promise<void> => {
    if (!ekyc) return
    setBusy(true); setErr('')
    try {
      const r = await api.ekycDecide(ekyc.id)
      setEkyc(r.data)
    } catch (e) { setErr(String(e)) } finally { setBusy(false) }
  }, [api, ekyc])

  useEffect(() => {
    if (step === 4 && ekyc && !ekyc.decision) void decide()
  }, [step, ekyc, decide])

  async function submitExtraDoc(): Promise<void> {
    if (!upFile) return
    setDocBusy(true); setDocErr(''); setDocMsg('')
    try {
      await api.submitDocument({
        member_id: session.memberId,
        kind: upKind,
        filename: upFile.name,
        mime: upFile.type || 'application/octet-stream',
        size: upFile.size,
        ref: upFile.name,
        sha256: await sha256Hex(upFile),
      })
      setDocMsg(t(locale, 'kyc.docs.submitted'))
      setUpFile(null)
      await refreshDocs()
    } catch (e) { setDocErr(String(e)) } finally { setDocBusy(false) }
  }

  const unavailable = (ekyc?.risk_flags ?? []).some(
    (f) => f === 'doc_intel_unavailable' || f === 'biometrics_unavailable',
  )

  return (
    <main className="view">
      <h2>{t(locale, 'kyc.title')}</h2>
      <p className="muted">{t(locale, 'kyc.intro')}</p>

      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap' }}>
        {([1, 2, 3, 4] as const).map((n) => (
          <span key={n} className="badge" style={n === step ? undefined : { opacity: 0.45 }}>
            {t(locale, n === 1 ? 'kyc.step.document' : n === 2 ? 'kyc.step.identity' : n === 3 ? 'kyc.step.selfie' : 'kyc.step.result')}
          </span>
        ))}
      </div>

      {step === 1 && (
        <section className="card">
          <h3>{t(locale, 'kyc.step')} 1 — {t(locale, 'kyc.step.document')}</h3>
          <label>
            {t(locale, 'kyc.doc.kind')}
            <select value={docKind} onChange={(e) => setDocKind(e.target.value as DocKind)}>
              {DOC_KINDS.map((k) => (
                <option key={k} value={k}>{t(locale, `kyc.kind.${k}`)}</option>
              ))}
            </select>
          </label>
          <label>
            {t(locale, 'kyc.doc.file')}
            <input type="file" accept="image/*,application/pdf" onChange={(e) => void onDocFile(e)} />
          </label>
          {tooLarge && (
            <div className="banner">
              {t(locale, 'kyc.doc.too_large')}
              <label style={{ flexDirection: 'row', gap: 8, alignItems: 'center', marginTop: 6 }}>
                <input type="checkbox" checked={refOnly} onChange={(e) => setRefOnly(e.target.checked)} />
                {t(locale, 'kyc.doc.ref_only')}
              </label>
            </div>
          )}
          <button
            className="primary"
            type="button"
            onClick={() => void submitDoc()}
            disabled={busy || !docFile || (tooLarge && !refOnly)}
          >
            {t(locale, 'kyc.doc.submit')}
          </button>
          {err && <p className="err">{err}</p>}
        </section>
      )}

      {step === 2 && (
        <section className="card">
          <h3>{t(locale, 'kyc.step')} 2 — {t(locale, 'kyc.step.identity')}</h3>
          <label>
            {t(locale, 'kyc.identity.nin')}
            <input inputMode="numeric" maxLength={11} value={nin} onChange={(e) => setNin(e.target.value)} />
          </label>
          <label>
            {t(locale, 'kyc.identity.bvn')}
            <input inputMode="numeric" maxLength={11} value={bvn} onChange={(e) => setBvn(e.target.value)} />
          </label>
          <label>
            {t(locale, 'kyc.identity.name')}
            <input value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label>
            {t(locale, 'kyc.identity.dob')}
            <input type="date" value={dob} onChange={(e) => setDob(e.target.value)} />
          </label>
          <label>
            {t(locale, 'kyc.identity.phone')}
            <input value={phone} onChange={(e) => setPhone(e.target.value)} />
          </label>
          {nin && !/^\d{11}$/.test(nin) && <p className="err">{t(locale, 'kyc.identity.nin_invalid')}</p>}
          <button
            className="primary"
            type="button"
            onClick={() => void submitIdentity()}
            disabled={busy || !/^\d{11}$/.test(nin) || !name.trim() || !dob}
          >
            {t(locale, 'kyc.identity.submit')}
          </button>
          {err && <p className="err">{err}</p>}
        </section>
      )}

      {step === 3 && (
        <section className="card">
          <h3>{t(locale, 'kyc.step')} 3 — {t(locale, 'kyc.step.selfie')}</h3>
          <p className="muted">{t(locale, 'kyc.selfie.intro')}</p>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {frames.map((f, i) => (
              <div key={i} style={{ width: 84 }}>
                <img src={f} alt="" style={{ width: 84, height: 84, objectFit: 'cover', borderRadius: 12 }} />
                <button type="button" className="ghost" onClick={() => setFrames(frames.filter((_, j) => j !== i))}>
                  {t(locale, 'kyc.selfie.remove')}
                </button>
              </div>
            ))}
          </div>
          {frames.length < 3 && (
            <label>
              {t(locale, 'kyc.selfie.add')}
              <input type="file" accept="image/*" capture="user" onChange={(e) => void onSelfie(e)} />
            </label>
          )}
          <button
            className="primary"
            type="button"
            onClick={() => void submitBiometric()}
            disabled={busy || frames.length === 0}
          >
            {t(locale, 'kyc.selfie.submit')}
          </button>
          {err && <p className="err">{err}</p>}
        </section>
      )}

      {step === 4 && ekyc && (
        <section className="card">
          <h3>{t(locale, 'kyc.step')} 4 — {t(locale, 'kyc.step.result')}</h3>
          {!ekyc.decision && (
            <button className="primary" type="button" onClick={() => void decide()} disabled={busy}>
              {busy ? t(locale, 'common.loading') : t(locale, 'kyc.result.check')}
            </button>
          )}
          {ekyc.decision === 'AUTO_APPROVE' && (
            <div className="banner ok">
              <b>{t(locale, 'kyc.result.approved')}</b>
              <p>{t(locale, 'kyc.result.approved_body')}</p>
            </div>
          )}
          {ekyc.decision === 'MANUAL_REVIEW' && (
            <div className="banner">
              <b>{t(locale, 'kyc.result.review')}</b>
              <p>{unavailable ? t(locale, 'kyc.result.unavailable') : t(locale, 'kyc.result.review_body')}</p>
              {ekyc.case_id && <p>{t(locale, 'kyc.result.case')}: {ekyc.case_id}</p>}
            </div>
          )}
          {ekyc.decision === 'REJECT' && (
            <div className="banner" style={{ borderLeftColor: '#b91c1c', background: '#fef2f2' }}>
              <b>{t(locale, 'kyc.result.rejected')}</b>
              {ekyc.risk_flags.length > 0 && (
                <p>{t(locale, 'kyc.result.flags')}: {ekyc.risk_flags.map(humaniseFlag).join(', ')}</p>
              )}
            </div>
          )}
          {err && <p className="err">{err}</p>}
        </section>
      )}

      {ekyc && (
        <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', fontSize: 12 }}>
          {ekyc.nin_verified && <span className="badge">✓ {t(locale, 'kyc.badge.nin')}</span>}
          {ekyc.bvn_verified && <span className="badge">✓ {t(locale, 'kyc.badge.bvn')}</span>}
          {ekyc.liveness_score > 0 && (
            <span className="badge">{t(locale, 'kyc.badge.liveness')} {scorePct(ekyc.liveness_score)}%</span>
          )}
          {ekyc.face_score > 0 && (
            <span className="badge">{t(locale, 'kyc.badge.face')} {scorePct(ekyc.face_score)}%</span>
          )}
        </div>
      )}

      <section className="card">
        <h3>{t(locale, 'kyc.docs.title')}</h3>
        <label>
          {t(locale, 'kyc.doc.kind')}
          <select value={upKind} onChange={(e) => setUpKind(e.target.value as DocKind)}>
            {DOC_KINDS.map((k) => (
              <option key={k} value={k}>{t(locale, `kyc.kind.${k}`)}</option>
            ))}
          </select>
        </label>
        <label>
          {t(locale, 'kyc.doc.file')}
          <input type="file" onChange={(e) => setUpFile(e.target.files?.[0] ?? null)} />
        </label>
        <button
          className="primary"
          type="button"
          onClick={() => void submitExtraDoc()}
          disabled={docBusy || !upFile}
        >
          {t(locale, 'kyc.docs.submit')}
        </button>
        {docMsg && <p className="ok">{docMsg}</p>}
        {docErr && <p className="err">{docErr}</p>}
        <ul className="lesson-list">
          {docs.map((d) => (
            <li key={d.id}>
              <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
                <div>
                  <b>{t(locale, `kyc.kind.${d.kind}`)}</b>
                  <div style={{ fontSize: 12, opacity: 0.75 }}>
                    {d.filename} · {new Date(d.submitted_at).toLocaleDateString()}
                  </div>
                </div>
                <span style={{
                  alignSelf: 'center', fontSize: 12, padding: '2px 8px', borderRadius: 10,
                  background: docChipBg(d.status),
                }}>
                  {d.status}
                </span>
              </div>
              {d.review_note && <p style={{ fontSize: 13, margin: '4px 0' }}>{d.review_note}</p>}
            </li>
          ))}
          {docs.length === 0 && <li>{t(locale, 'kyc.docs.empty')}</li>}
        </ul>
      </section>
    </main>
  )
}
