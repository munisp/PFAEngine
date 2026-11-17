import { useCallback, useEffect, useState } from 'react';
import { Api } from '../api';
import type { StaffDoc } from '../api';

// Document review — maker-checker queue for member-uploaded KYC/claim
// documents. Review decision posts back and removes the item from the queue.
export default function DocReview({ api }: { api: Api }) {
  const [docs, setDocs] = useState<StaffDoc[]>([]);
  const [sel, setSel] = useState<StaffDoc | null>(null);
  const [note, setNote] = useState('');
  const [err, setErr] = useState('');
  const [toast, setToast] = useState('');

  const refresh = useCallback(async () => {
    try { setDocs((await api.docsPending()).documents); setErr(''); }
    catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const review = async (approve: boolean) => {
    if (!sel) return;
    try {
      await api.docReview(sel.id, approve, note);
      setToast(`Document ${sel.filename} ${approve ? 'approved' : 'rejected'}.`);
      setSel(null);
      setNote('');
      setErr('');
      await refresh();
    } catch (e: any) { setErr(e.message); }
  };

  return (
    <section>
      <h2>Document review</h2>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      {toast && <p style={{ background: '#f0fdf4', padding: 8, borderRadius: 6 }}>{toast}</p>}
      {docs.length === 0 && !err && <p style={{ color: '#6b7280' }}>No documents pending review.</p>}
      <div style={{ display: 'flex', gap: 16 }}>
        {docs.length > 0 && (
          <table style={{ flex: 1, borderCollapse: 'collapse' }}>
            <thead><tr>
              <Th>Member</Th><Th>Kind</Th><Th>Filename</Th><Th>Size</Th><Th>Submitted</Th>
            </tr></thead>
            <tbody>
              {docs.map(d => (
                <tr key={d.id} onClick={() => { setSel(d); setNote(''); setToast(''); }}
                  style={{ cursor: 'pointer', background: sel?.id === d.id ? '#f3f4f6' : undefined }}>
                  <Td>{d.member_id}</Td>
                  <Td><span style={{ background: '#eef2ff', borderRadius: 4, padding: '2px 6px' }}>{d.kind}</span></Td>
                  <Td>{d.filename}</Td>
                  <Td>{(d.size / 1024).toFixed(1)} KB</Td>
                  <Td>{new Date(d.submitted_at).toLocaleString()}</Td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {sel && (
          <aside style={{ width: 380, border: '1px solid #e5e7eb', borderRadius: 8, padding: 12 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between' }}>
              <h3 style={{ margin: 0 }}>{sel.filename}</h3>
              <button onClick={() => setSel(null)}>×</button>
            </div>
            <p style={{ fontSize: 13, margin: '4px 0' }}>
              {sel.id} · {sel.kind} · {sel.mime} · {(sel.size / 1024).toFixed(1)} KB
            </p>
            <p style={{ fontSize: 13, margin: '4px 0' }}>Member: {sel.member_id}</p>
            <p style={{ fontSize: 13, margin: '4px 0' }}>Submitted: {new Date(sel.submitted_at).toLocaleString()}</p>
            <p style={{ fontSize: 13, margin: '4px 0' }}>
              Ref: <a href={sel.ref} target="_blank" rel="noreferrer">{sel.ref}</a>
            </p>
            <p style={{ fontSize: 12, margin: '4px 0', wordBreak: 'break-all', color: '#6b7280' }}>SHA-256: {sel.sha256}</p>
            <h4>Decision</h4>
            <input placeholder="Review note" value={note} onChange={e => setNote(e.target.value)} style={{ width: '100%', marginBottom: 8 }} />
            <div style={{ display: 'flex', gap: 8 }}>
              <button onClick={() => review(true)} style={{ background: '#16a34a', color: 'white', border: 0, borderRadius: 4, padding: '6px 12px' }}>Approve</button>
              <button onClick={() => review(false)} style={{ background: '#dc2626', color: 'white', border: 0, borderRadius: 4, padding: '6px 12px' }}>Reject</button>
            </div>
          </aside>
        )}
      </div>
    </section>
  );
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children }: { children: React.ReactNode }) =>
  <td style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
