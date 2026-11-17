import { useCallback, useEffect, useState } from 'react';
import { Api } from '../api';
import type { EkycHealth, EkycSession, KybBiz, KybInput } from '../api';

// KYC / KYB ops — eKYC session monitoring with AI-sidecar health banner and
// manual-review overrides, plus KYB business verification submission, screening
// results and manual review decisions.
export default function KycOps({ api }: { api: Api }) {
  const [tab, setTab] = useState<'ekyc' | 'kyb'>('ekyc');
  return (
    <section>
      <h2>KYC / KYB ops</h2>
      <nav style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <button onClick={() => setTab('ekyc')} style={{ fontWeight: tab === 'ekyc' ? 700 : 400 }}>eKYC sessions</button>
        <button onClick={() => setTab('kyb')} style={{ fontWeight: tab === 'kyb' ? 700 : 400 }}>KYB businesses</button>
      </nav>
      {tab === 'ekyc' ? <EkycTab api={api} /> : <KybTab api={api} />}
    </section>
  );
}

const EKYC_FILTERS = ['DOCUMENT_CAPTURE', 'DECIDED', 'MANUAL_REVIEW', 'AUTO_APPROVE', 'REJECT'];

function EkycTab({ api }: { api: Api }) {
  const [health, setHealth] = useState<EkycHealth | null>(null);
  const [sessions, setSessions] = useState<EkycSession[]>([]);
  const [filter, setFilter] = useState('');
  const [sel, setSel] = useState<EkycSession | null>(null);
  const [note, setNote] = useState('');
  const [err, setErr] = useState('');

  const refresh = useCallback(async () => {
    try {
      const [h, s] = await Promise.all([
        api.ekycHealth().catch(() => ({ ok: false, error: 'unreachable' }) as EkycHealth),
        api.ekycSessions(),
      ]);
      setHealth(h);
      setSessions(s.sessions);
      setErr('');
    } catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const override = async (approve: boolean) => {
    if (!sel) return;
    try {
      await api.ekycOverride(sel.id, approve, note);
      setSel(null); setNote(''); setErr('');
      await refresh();
    } catch (e: any) { setErr(e.message); }
  };

  const visible = sessions.filter(s =>
    !filter || s.state === filter || s.decision === filter);

  return (
    <>
      {health && (
        <div style={{
          borderRadius: 6, padding: 8, marginBottom: 12, fontSize: 13,
          background: health.ok ? '#f0fdf4' : '#fffbeb',
          border: `1px solid ${health.ok ? '#86efac' : '#fcd34d'}`,
        }}>
          {health.ok ? (
            <>
              ✅ AI sidecar online
              {health.models_present && (
                <span style={{ marginLeft: 8 }}>
                  {Object.entries(health.models_present).map(([m, present]) => (
                    <span key={m} style={{
                      background: present ? '#dcfce7' : '#fee2e2', borderRadius: 4,
                      padding: '2px 6px', marginRight: 4, fontSize: 12,
                    }}>
                      {m}{present ? '' : ' (missing)'}
                    </span>
                  ))}
                </span>
              )}
            </>
          ) : (
            <>⚠ Sidecar offline — sessions route to manual review{health.error ? ` (${health.error})` : ''}</>
          )}
        </div>
      )}

      <div style={{ display: 'flex', gap: 6, marginBottom: 12, flexWrap: 'wrap' }}>
        <button onClick={() => setFilter('')} style={{ fontWeight: filter === '' ? 700 : 400 }}>All</button>
        {EKYC_FILTERS.map(f => (
          <button key={f} onClick={() => setFilter(filter === f ? '' : f)}
            style={{ fontWeight: filter === f ? 700 : 400, borderRadius: 12 }}>{f}</button>
        ))}
      </div>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}

      <div style={{ display: 'flex', gap: 16 }}>
        <table style={{ flex: 1, borderCollapse: 'collapse' }}>
          <thead><tr>
            <Th>ID</Th><Th>Member</Th><Th>State</Th><Th>Decision</Th><Th>Risk</Th><Th>Flags</Th>
          </tr></thead>
          <tbody>
            {visible.map(s => (
              <tr key={s.id} onClick={() => { setSel(s); setNote(''); }}
                style={{ cursor: 'pointer', background: sel?.id === s.id ? '#f3f4f6' : undefined }}>
                <Td>{s.id}</Td><Td>{s.member_id}</Td><Td>{s.state}</Td>
                <Td><DecisionBadge decision={s.decision} /></Td>
                <Td>{s.risk_score}</Td>
                <Td>{(s.risk_flags || []).map(f => (
                  <span key={f} style={{ background: '#fee2e2', borderRadius: 4, padding: '1px 5px', marginRight: 4, fontSize: 12 }}>{f}</span>
                ))}</Td>
              </tr>
            ))}
            {visible.length === 0 && <tr><Td colSpan={6}>No sessions match.</Td></tr>}
          </tbody>
        </table>

        {sel && (
          <aside style={{ width: 380, border: '1px solid #e5e7eb', borderRadius: 8, padding: 12 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}>
              <h3 style={{ margin: 0 }}>{sel.id}</h3>
              <button onClick={() => setSel(null)}>×</button>
            </div>
            <p style={{ fontSize: 13, margin: '4px 0' }}>
              {sel.member_id} · {sel.state} · <DecisionBadge decision={sel.decision} />
              {sel.case_id && <span style={{ color: '#6b7280' }}> · case {sel.case_id}</span>}
            </p>
            <p style={{ fontSize: 13, margin: '4px 0' }}>
              Started {new Date(sel.created_at).toLocaleString()}
            </p>
            <h4>Verification scores</h4>
            <p style={{ fontSize: 13, margin: '4px 0' }}>
              NIN {sel.nin_verified ? '✅' : '❌'} · BVN {sel.bvn_verified ? '✅' : '❌'} ·
              Liveness {(sel.liveness_score * 100).toFixed(1)}% · Face cosine {sel.face_score.toFixed(3)} ·
              Risk {sel.risk_score}
            </p>
            {(sel.risk_flags || []).length > 0 && (
              <p style={{ fontSize: 13, margin: '4px 0' }}>
                {(sel.risk_flags || []).map(f => (
                  <span key={f} style={{ background: '#fee2e2', borderRadius: 4, padding: '2px 6px', marginRight: 4 }}>{f}</span>
                ))}
              </p>
            )}
            <h4>History</h4>
            <ul style={{ paddingLeft: 16, margin: '4px 0', maxHeight: 160, overflow: 'auto' }}>
              {(sel.history || []).map((h, i) => (
                <li key={i} style={{ fontSize: 12 }}>
                  {new Date(h.at).toLocaleString()} — <b>{h.actor}</b> {h.action}
                  {h.detail && <span style={{ color: '#6b7280' }}> ({h.detail})</span>}
                </li>
              ))}
            </ul>
            {sel.decision === 'MANUAL_REVIEW' && (
              <>
                <h4>Override decision</h4>
                <input placeholder="Decision note" value={note} onChange={e => setNote(e.target.value)}
                  style={{ width: '100%', marginBottom: 8 }} />
                <div style={{ display: 'flex', gap: 8 }}>
                  <button onClick={() => override(true)}
                    style={{ background: '#16a34a', color: 'white', border: 0, borderRadius: 4, padding: '6px 12px' }}>Approve</button>
                  <button onClick={() => override(false)}
                    style={{ background: '#dc2626', color: 'white', border: 0, borderRadius: 4, padding: '6px 12px' }}>Reject</button>
                </div>
              </>
            )}
          </aside>
        )}
      </div>
    </>
  );
}

function DecisionBadge({ decision }: { decision: string }) {
  const colors: Record<string, string> = {
    AUTO_APPROVE: '#f0fdf4', MANUAL_REVIEW: '#fef9c3', REJECT: '#fee2e2', PENDING: '#eef2ff',
  };
  return <span style={{ background: colors[decision] ?? '#f3f4f6', borderRadius: 4, padding: '2px 6px' }}>{decision || '—'}</span>;
}

type DirectorRow = { name: string; dob: string; role: string };
type UboRow = { name: string; dob: string; ownership_pct: string };

function KybTab({ api }: { api: Api }) {
  const [biz, setBiz] = useState<KybBiz[]>([]);
  const [showForm, setShowForm] = useState(false);
  const [sel, setSel] = useState<KybBiz | null>(null);
  const [note, setNote] = useState('');
  const [err, setErr] = useState('');
  const [form, setForm] = useState({ employer_id: '', name: '', rc_number: '', tin: '', doc_refs: '' });
  const [directors, setDirectors] = useState<DirectorRow[]>([{ name: '', dob: '', role: 'Director' }]);
  const [ubos, setUbos] = useState<UboRow[]>([{ name: '', dob: '', ownership_pct: '' }]);

  const refresh = useCallback(async () => {
    try { setBiz((await api.kybList()).businesses); setErr(''); }
    catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const submit = async () => {
    const body: KybInput = {
      employer_id: form.employer_id,
      name: form.name,
      rc_number: form.rc_number,
      tin: form.tin,
      directors: directors.filter(d => d.name).map(d => ({ name: d.name, dob: d.dob, role: d.role })),
      ubos: ubos.filter(u => u.name).map(u => ({ name: u.name, dob: u.dob, ownership_pct: Number(u.ownership_pct) || 0 })),
      doc_refs: form.doc_refs.split(',').map(s => s.trim()).filter(Boolean),
    };
    try {
      await api.kybSubmit(body);
      setForm({ employer_id: '', name: '', rc_number: '', tin: '', doc_refs: '' });
      setDirectors([{ name: '', dob: '', role: 'Director' }]);
      setUbos([{ name: '', dob: '', ownership_pct: '' }]);
      setShowForm(false);
      setErr('');
      await refresh();
    } catch (e: any) { setErr(e.message); }
  };

  const review = async (id: string, approve: boolean) => {
    try {
      await api.kybReview(id, approve, note);
      setNote(''); setErr('');
      await refresh();
      if (sel?.id === id) setSel(null);
    } catch (e: any) { setErr(e.message); }
  };

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3>Businesses</h3>
        <button onClick={() => setShowForm(!showForm)}>{showForm ? 'Hide form' : 'New verification'}</button>
      </div>
      {showForm && (
        <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: 12, marginBottom: 12 }}>
          <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 8 }}>
            <input placeholder="Employer ID" value={form.employer_id} onChange={e => setForm({ ...form, employer_id: e.target.value })} />
            <input placeholder="Business name" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
            <input placeholder="RC number" value={form.rc_number} onChange={e => setForm({ ...form, rc_number: e.target.value })} />
            <input placeholder="TIN" value={form.tin} onChange={e => setForm({ ...form, tin: e.target.value })} />
            <input placeholder="Doc refs (comma-separated)" value={form.doc_refs} onChange={e => setForm({ ...form, doc_refs: e.target.value })} style={{ width: 220 }} />
          </div>
          <h4 style={{ margin: '8px 0 4px' }}>Directors</h4>
          {directors.map((d, i) => (
            <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 4 }}>
              <input placeholder="Name" value={d.name} onChange={e => setDirectors(directors.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} />
              <input placeholder="DOB (YYYY-MM-DD)" value={d.dob} onChange={e => setDirectors(directors.map((x, j) => j === i ? { ...x, dob: e.target.value } : x))} />
              <input placeholder="Role" value={d.role} onChange={e => setDirectors(directors.map((x, j) => j === i ? { ...x, role: e.target.value } : x))} />
              <button onClick={() => setDirectors(directors.filter((_, j) => j !== i))} disabled={directors.length === 1}>×</button>
            </div>
          ))}
          <button onClick={() => setDirectors([...directors, { name: '', dob: '', role: 'Director' }])}>+ director</button>
          <h4 style={{ margin: '8px 0 4px' }}>UBOs</h4>
          {ubos.map((u, i) => (
            <div key={i} style={{ display: 'flex', gap: 6, marginBottom: 4 }}>
              <input placeholder="Name" value={u.name} onChange={e => setUbos(ubos.map((x, j) => j === i ? { ...x, name: e.target.value } : x))} />
              <input placeholder="DOB (YYYY-MM-DD)" value={u.dob} onChange={e => setUbos(ubos.map((x, j) => j === i ? { ...x, dob: e.target.value } : x))} />
              <input placeholder="Ownership %" type="number" value={u.ownership_pct} onChange={e => setUbos(ubos.map((x, j) => j === i ? { ...x, ownership_pct: e.target.value } : x))} style={{ width: 110 }} />
              <button onClick={() => setUbos(ubos.filter((_, j) => j !== i))} disabled={ubos.length === 1}>×</button>
            </div>
          ))}
          <button onClick={() => setUbos([...ubos, { name: '', dob: '', ownership_pct: '' }])}>+ UBO</button>
          <div style={{ marginTop: 8 }}>
            <button onClick={submit} disabled={!form.employer_id || !form.name}>Submit verification</button>
          </div>
        </div>
      )}
      {err && <p style={{ color: 'crimson' }}>{err}</p>}

      <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
        <thead><tr><Th>Business</Th><Th>RC</Th><Th>TIN</Th><Th>Risk</Th><Th>Status</Th><Th>Actions</Th></tr></thead>
        <tbody>
          {biz.map(b => (
            <tr key={b.id} onClick={() => { setSel(sel?.id === b.id ? null : b); setNote(''); }}
              style={{ cursor: 'pointer', background: sel?.id === b.id ? '#f3f4f6' : undefined }}>
              <Td>{b.name}</Td><Td>{b.rc_number}</Td><Td>{b.tin}</Td><Td>{b.risk_score}</Td>
              <Td><KybStatusBadge status={b.status} /></Td>
              <td onClick={e => e.stopPropagation()} style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>
                {b.status === 'MANUAL_REVIEW' && (
                  <span style={{ display: 'flex', gap: 4 }}>
                    <input placeholder="Note" value={note} onChange={e => setNote(e.target.value)} style={{ width: 120 }} />
                    <button onClick={() => review(b.id, true)}>Approve</button>
                    <button onClick={() => review(b.id, false)}>Reject</button>
                  </span>
                )}
              </td>
            </tr>
          ))}
          {biz.length === 0 && <tr><Td colSpan={6}>No businesses submitted.</Td></tr>}
        </tbody>
      </table>

      {sel && (
        <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: 12 }}>
          <h3 style={{ marginTop: 0 }}>{sel.name} <KybStatusBadge status={sel.status} /></h3>
          <p style={{ fontSize: 13 }}>
            {sel.employer_id} · RC {sel.rc_number} · TIN {sel.tin} · Risk {sel.risk_score}
            {sel.case_id && <span style={{ color: '#6b7280' }}> · case {sel.case_id}</span>}
          </p>
          {(sel.risk_flags || []).length > 0 && (
            <p style={{ fontSize: 13 }}>
              {(sel.risk_flags || []).map(f => (
                <span key={f} style={{ background: '#fee2e2', borderRadius: 4, padding: '2px 6px', marginRight: 4 }}>{f}</span>
              ))}
            </p>
          )}
          <h4>Directors &amp; UBOs</h4>
          <table style={{ borderCollapse: 'collapse' }}>
            <thead><tr><Th>Name</Th><Th>DOB</Th><Th>Role / Ownership</Th><Th>Screening</Th></tr></thead>
            <tbody>
              {(sel.directors || []).map((d, i) => (
                <tr key={`d-${i}`}>
                  <Td>{d.name}</Td><Td>{d.dob}</Td><Td>{d.role ?? 'Director'}</Td>
                  <Td><ScreeningBadges screening={sel.screening} name={d.name} /></Td>
                </tr>
              ))}
              {(sel.ubos || []).map((u, i) => (
                <tr key={`u-${i}`}>
                  <Td>{u.name}</Td><Td>{u.dob}</Td><Td>UBO {u.ownership_pct}%</Td>
                  <Td><ScreeningBadges screening={sel.screening} name={u.name} /></Td>
                </tr>
              ))}
            </tbody>
          </table>
          {(sel.doc_refs || []).length > 0 && (
            <p style={{ fontSize: 12, color: '#6b7280' }}>Docs: {(sel.doc_refs || []).join(', ')}</p>
          )}
        </div>
      )}
    </>
  );
}

function ScreeningBadges({ screening, name }: { screening: KybBiz['screening']; name: string }) {
  // Screening entries are keyed by person_id; match by name where possible,
  // otherwise show nothing (screening may still be pending).
  const s = (screening || []).find(x => x.person_id === name);
  if (!s) return <span style={{ color: '#9ca3af', fontSize: 12 }}>—</span>;
  return (
    <span style={{ fontSize: 12 }}>
      {s.pep && <span style={{ background: '#fef9c3', borderRadius: 4, padding: '1px 5px', marginRight: 4 }}>PEP</span>}
      {s.sanction_hit && <span style={{ background: '#fee2e2', borderRadius: 4, padding: '1px 5px' }}>SANCTION</span>}
      {!s.pep && !s.sanction_hit && <span style={{ color: '#16a34a' }}>clear</span>}
    </span>
  );
}

function KybStatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    PENDING: '#eef2ff', VERIFIED: '#f0fdf4', MANUAL_REVIEW: '#fef9c3', REJECTED: '#fee2e2',
  };
  return <span style={{ background: colors[status] ?? '#f3f4f6', borderRadius: 4, padding: '2px 6px' }}>{status}</span>;
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children, colSpan }: { children: React.ReactNode; colSpan?: number }) =>
  <td colSpan={colSpan} style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
