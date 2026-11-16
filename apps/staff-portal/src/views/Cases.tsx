import { useCallback, useEffect, useState } from 'react';
import { Api } from '../api';
import type { CaseRow, CaseStats } from '../api';

// Cases workbench — the unified operational queue (Tranche 20). Filters by
// queue/type/status, SLA breach sweep, and a detail panel with comments,
// tasks, assignment, escalation and task-gated resolution.
export default function Cases({ api }: { api: Api }) {
  const [cases, setCases] = useState<CaseRow[]>([]);
  const [stats, setStats] = useState<CaseStats | null>(null);
  const [queue, setQueue] = useState('');
  const [type, setType] = useState('');
  const [openOnly, setOpenOnly] = useState(true);
  const [breached, setBreached] = useState(false);
  const [sel, setSel] = useState<CaseRow | null>(null);
  const [err, setErr] = useState('');

  const refresh = useCallback(async () => {
    try {
      const [l, s] = await Promise.all([
        api.casesList({ queue: queue || undefined, type: type || undefined, open: openOnly, breached }),
        api.caseStats(),
      ]);
      setCases(l.cases);
      setStats(s);
      setErr('');
    } catch (e: any) { setErr(e.message); }
  }, [api, queue, type, openOnly, breached]);

  useEffect(() => { refresh(); }, [refresh]);

  const act = async (fn: () => Promise<any>) => {
    try { await fn(); setErr(''); await refresh(); if (sel) setSel(await api.caseGet(sel.id)); }
    catch (e: any) { setErr(e.message); }
  };

  return (
    <section>
      <h2>Case workbench</h2>
      {stats && (
        <div style={{ display: 'flex', gap: 16, marginBottom: 12 }}>
          <Stat label="Open" v={stats.open} />
          <Stat label="SLA breached" v={stats.breached} warn={stats.breached > 0} />
          <Stat label="Avg resolve (h)" v={stats.avg_resolve_hours.toFixed(1)} />
          <Stat label="CSAT" v={stats.csat_count ? stats.csat_avg.toFixed(1) + '/5' : '—'} />
          <Stat label="Aged 7d+" v={stats.open_by_age_days['7+'] ?? 0} warn={(stats.open_by_age_days['7+'] ?? 0) > 0} />
        </div>
      )}
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <select value={queue} onChange={e => setQueue(e.target.value)}>
          <option value="">All queues</option>
          {['FRAUD_OPS', 'MEMBER_SERVICES', 'BENEFITS', 'COMPLIANCE', 'OPERATIONS'].map(q => <option key={q}>{q}</option>)}
        </select>
        <select value={type} onChange={e => setType(e.target.value)}>
          <option value="">All types</option>
          {['FRAUD', 'COMPLAINT', 'CLAIM', 'DEATH_CLAIM', 'SERVICE_REQUEST', 'KYC_REVIEW', 'RECON_BREAK', 'AML_ALERT', 'GENERAL'].map(t => <option key={t}>{t}</option>)}
        </select>
        <label><input type="checkbox" checked={openOnly} onChange={e => setOpenOnly(e.target.checked)} /> Open only</label>
        <label><input type="checkbox" checked={breached} onChange={e => setBreached(e.target.checked)} /> SLA breached</label>
        <button onClick={() => act(() => api.caseSweep())}>Run SLA sweep</button>
      </div>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      <div style={{ display: 'flex', gap: 16 }}>
        <table style={{ flex: 1, borderCollapse: 'collapse' }}>
          <thead><tr>
            <Th>ID</Th><Th>Type</Th><Th>Subject</Th><Th>Pri</Th><Th>Status</Th><Th>Assignee</Th><Th>Due</Th>
          </tr></thead>
          <tbody>
            {cases.map(c => (
              <tr key={c.id} onClick={() => api.caseGet(c.id).then(setSel)}
                style={{ cursor: 'pointer', background: sel?.id === c.id ? '#f3f4f6' : c.sla_breached ? '#fef2f2' : undefined }}>
                <Td>{c.id}</Td><Td>{c.type}</Td><Td>{c.subject}</Td>
                <Td>{c.priority}</Td><Td>{c.status}</Td><Td>{c.assignee || '—'}</Td>
                <Td>{new Date(c.due_at).toLocaleDateString()}</Td>
              </tr>
            ))}
            {cases.length === 0 && <tr><Td colSpan={7}>No cases match.</Td></tr>}
          </tbody>
        </table>
        {sel && <CaseDetail api={api} c={sel} onAct={act} onClose={() => setSel(null)} />}
      </div>
    </section>
  );
}

function Stat({ label, v, warn }: { label: string; v: React.ReactNode; warn?: boolean }) {
  return (
    <div style={{ padding: '8px 12px', border: '1px solid #e5e7eb', borderRadius: 8, background: warn ? '#fef2f2' : '#fafafa' }}>
      <div style={{ fontSize: 12, color: '#6b7280' }}>{label}</div>
      <div style={{ fontSize: 20, fontWeight: 600 }}>{v}</div>
    </div>
  );
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children, colSpan }: { children: React.ReactNode; colSpan?: number }) =>
  <td colSpan={colSpan} style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;

function CaseDetail({ api, c, onAct, onClose }: { api: Api; c: CaseRow; onAct: (fn: () => Promise<any>) => Promise<void>; onClose: () => void }) {
  const [comment, setComment] = useState('');
  const [task, setTask] = useState('');
  const [assignee, setAssignee] = useState(c.assignee || '');
  const [resolution, setResolution] = useState('');
  const [code, setCode] = useState('UPHELD');
  const terminal = c.status === 'RESOLVED' || c.status === 'CLOSED';

  return (
    <aside style={{ width: 380, border: '1px solid #e5e7eb', borderRadius: 8, padding: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        <h3 style={{ margin: 0 }}>{c.id}</h3>
        <button onClick={onClose}>×</button>
      </div>
      <p style={{ margin: '4px 0' }}><b>{c.subject}</b></p>
      <p style={{ fontSize: 13, color: '#4b5563' }}>{c.description}</p>
      <p style={{ fontSize: 12 }}>
        {c.type} · {c.priority} · {c.queue} · {c.status}
        {c.sla_breached && <b style={{ color: 'crimson' }}> · SLA BREACHED</b>}
      </p>
      {c.member_id && <p style={{ fontSize: 12 }}>Member: {c.member_id}</p>}
      {c.linked_id && <p style={{ fontSize: 12 }}>Linked: {c.linked_kind}/{c.linked_id}</p>}

      <h4>Tasks</h4>
      <ul style={{ paddingLeft: 16, margin: '4px 0' }}>
        {(c.tasks || []).map(t => (
          <li key={t.id} style={{ fontSize: 13 }}>
            {t.done ? '✅' : '⬜'} {t.title}
            {!t.done && <button style={{ marginLeft: 6 }} onClick={() => onAct(() => api.caseCompleteTask(c.id, t.id))}>done</button>}
          </li>
        ))}
      </ul>
      {!terminal && (
        <div style={{ display: 'flex', gap: 4 }}>
          <input placeholder="New task" value={task} onChange={e => setTask(e.target.value)} style={{ flex: 1 }} />
          <button onClick={() => { if (task) onAct(() => api.caseAddTask(c.id, task)).then(() => setTask('')); }}>Add</button>
        </div>
      )}

      <h4>Comments</h4>
      <ul style={{ paddingLeft: 16, margin: '4px 0', maxHeight: 120, overflow: 'auto' }}>
        {(c.comments || []).map((cm, i) => (
          <li key={i} style={{ fontSize: 13 }}><b>{cm.author}</b>: {cm.body}</li>
        ))}
      </ul>
      <div style={{ display: 'flex', gap: 4 }}>
        <input placeholder="Comment" value={comment} onChange={e => setComment(e.target.value)} style={{ flex: 1 }} />
        <button onClick={() => { if (comment) onAct(() => api.caseComment(c.id, comment)).then(() => setComment('')); }}>Post</button>
      </div>

      {!terminal && (
        <>
          <h4>Actions</h4>
          <div style={{ display: 'flex', gap: 4, marginBottom: 6 }}>
            <input placeholder="Assignee" value={assignee} onChange={e => setAssignee(e.target.value)} style={{ flex: 1 }} />
            <button onClick={() => onAct(() => api.caseAssign(c.id, assignee))}>Assign</button>
          </div>
          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
            <button onClick={() => onAct(() => api.caseTransition(c.id, 'IN_PROGRESS', ''))}>Start work</button>
            <button onClick={() => onAct(() => api.caseTransition(c.id, 'PENDING_MEMBER', ''))}>Pending member</button>
            <button onClick={() => onAct(() => api.caseEscalate(c.id, 'manual escalation'))}>Escalate</button>
          </div>
          <h4>Resolve</h4>
          <div style={{ display: 'flex', gap: 4 }}>
            <select value={code} onChange={e => setCode(e.target.value)}>
              {['UPHELD', 'REJECTED', 'WITHDRAWN', 'CONFIRMED_FRAUD', 'CLEARED', 'FIXED', 'DUPLICATE'].map(x => <option key={x}>{x}</option>)}
            </select>
            <input placeholder="Resolution" value={resolution} onChange={e => setResolution(e.target.value)} style={{ flex: 1 }} />
            <button onClick={() => { if (resolution) onAct(() => api.caseResolve(c.id, resolution, code)); }}>Resolve</button>
          </div>
        </>
      )}
      {terminal && c.resolution && (
        <p style={{ fontSize: 13, background: '#f0fdf4', padding: 8, borderRadius: 6 }}>
          {c.resolution_code}: {c.resolution}{c.csat ? ` · CSAT ${c.csat}/5` : ''}
        </p>
      )}
    </aside>
  );
}
