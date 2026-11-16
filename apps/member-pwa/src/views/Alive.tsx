import { useEffect, useRef, useState } from 'react'
import type { Api } from '../api'
import type { Session } from '../session'
import type { Locale } from '../i18n'
import { t } from '../i18n'

interface Props {
  api: Api;
  session: Session;
  locale: Locale;
}

type Stage = 'ready' | 'camera' | 'preview' | 'success' | 'scheduled'

const QUEUE_KEY = 'pfa.alive.queue';

function enqueue(memberId: string, dataUrl: string): void {
  try {
    const raw = localStorage.getItem(QUEUE_KEY);
    const queue = raw ? (JSON.parse(raw) as unknown[]) : [];
    queue.push({ memberId, dataUrl, at: new Date().toISOString() });
    localStorage.setItem(QUEUE_KEY, JSON.stringify(queue));
  } catch {
    // storage unavailable — the user still sees the scheduled state
  }
}

// I-Am-Alive liveness confirmation: selfie capture via getUserMedia with a
// file-input fallback, then a stub POST (the member-facing liveness route
// ships with the benefit service); unreachable endpoints are queued locally
// and reported as "scheduled".
export default function Alive({ api, session, locale }: Props) {
  const [stage, setStage] = useState<Stage>('ready')
  const [image, setImage] = useState('')
  const [source, setSource] = useState<'selfie' | 'file'>('selfie')
  const [busy, setBusy] = useState(false)
  const [cameraFailed, setCameraFailed] = useState(false)
  const videoRef = useRef<HTMLVideoElement | null>(null)
  const streamRef = useRef<MediaStream | null>(null)

  useEffect(() => () => stopCamera(), [])

  function stopCamera(): void {
    streamRef.current?.getTracks().forEach((track) => track.stop())
    streamRef.current = null
  }

  async function openCamera(): Promise<void> {
    setCameraFailed(false)
    try {
      const stream = await navigator.mediaDevices.getUserMedia({
        video: { facingMode: 'user' },
        audio: false,
      })
      streamRef.current = stream
      if (videoRef.current) {
        videoRef.current.srcObject = stream
        await videoRef.current.play()
      }
      setStage('camera')
    } catch {
      // No camera / permission denied — the file input fallback is shown.
      setCameraFailed(true)
    }
  }

  function capture(): void {
    const video = videoRef.current
    if (!video) return
    const canvas = document.createElement('canvas')
    canvas.width = video.videoWidth || 640
    canvas.height = video.videoHeight || 480
    canvas.getContext('2d')?.drawImage(video, 0, 0)
    setImage(canvas.toDataURL('image/jpeg', 0.85))
    setSource('selfie')
    stopCamera()
    setStage('preview')
  }

  function onFile(ev: React.ChangeEvent<HTMLInputElement>): void {
    const file = ev.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      setImage(String(reader.result))
      setSource('file')
      setStage('preview')
    }
    reader.readAsDataURL(file)
  }

  async function submit(): Promise<void> {
    setBusy(true)
    try {
      await api.submitAliveProof(session.memberId, {
        proof: source,
        captured_at: new Date().toISOString(),
        image_data_url: image,
      })
      setStage('success')
    } catch {
      // Stub endpoint not live yet (or offline): queue and report scheduled.
      enqueue(session.memberId, image)
      setStage('scheduled')
    } finally {
      setBusy(false)
    }
  }

  if (stage === 'success' || stage === 'scheduled') {
    return (
      <div className={`card`}>
        <h1>{t(locale, 'i_am_alive')}</h1>
        {image && <div className="video-wrap"><img src={image} alt="" /></div>}
        <div className={`banner ${stage === 'success' ? 'ok' : ''}`}>
          {t(locale, stage === 'success' ? 'i_am_alive.success' : 'i_am_alive.scheduled')}
        </div>
        <button className="secondary" type="button" onClick={() => { setImage(''); setStage('ready') }}>
          {t(locale, 'i_am_alive.retake')}
        </button>
      </div>
    )
  }

  return (
    <div className="card">
      <h1>{t(locale, 'i_am_alive')}</h1>
      <p className="muted">{t(locale, 'i_am_alive.intro')}</p>
      {stage === 'camera' && (
        <div className="video-wrap"><video ref={videoRef} playsInline muted /></div>
      )}
      {stage === 'preview' && image && (
        <div className="video-wrap"><img src={image} alt="" /></div>
      )}
      {stage === 'ready' && (
        <button className="primary" type="button" onClick={() => void openCamera()}>
          {t(locale, 'i_am_alive.capture')}
        </button>
      )}
      {stage === 'camera' && (
        <button className="primary" type="button" onClick={capture}>
          {t(locale, 'i_am_alive.capture')}
        </button>
      )}
      {stage === 'preview' && (
        <>
          <button className="primary" type="button" onClick={() => void submit()} disabled={busy}>
            {t(locale, 'i_am_alive.submit')}
          </button>
          <button className="secondary" type="button" onClick={() => { setImage(''); setStage('ready') }}>
            {t(locale, 'i_am_alive.retake')}
          </button>
        </>
      )}
      {(cameraFailed || stage !== 'camera') && (
        <label>
          {t(locale, 'i_am_alive.upload')}
          <input type="file" accept="image/*" capture="user" onChange={onFile} />
        </label>
      )}
    </div>
  )
}
