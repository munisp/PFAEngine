import { useCallback, useEffect, useState } from 'react';
import { Api } from '../api';
import type { Article, Disclosure } from '../api';

// Content & disclosures — news CMS (draft → publish → archive) and the WORM
// regulatory disclosures ledger (audited RSAs, company accounts, returns).
export default function Content({ api }: { api: Api }) {
  const [tab, setTab] = useState<'news' | 'disclosures'>('news');
  return (
    <section>
      <h2>Content &amp; disclosures</h2>
      <nav style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <button onClick={() => setTab('news')} style={{ fontWeight: tab === 'news' ? 700 : 400 }}>News CMS</button>
        <button onClick={() => setTab('disclosures')} style={{ fontWeight: tab === 'disclosures' ? 700 : 400 }}>Disclosures</button>
      </nav>
      {tab === 'news' ? <NewsCMS api={api} /> : <Disclosures api={api} />}
    </section>
  );
}

const CATEGORIES = ['PENCOM_NOTICE', 'INDUSTRY', 'PRODUCT', 'EDUCATION'];
const KINDS = ['AUDITED_RSA_FUND', 'AUDITED_COMPANY', 'ANNUAL_REPORT', 'QUARTERLY_RETURNS'];

function NewsCMS({ api }: { api: Api }) {
  const [drafts, setDrafts] = useState<Article[]>([]);
  const [feed, setFeed] = useState<Article[]>([]);
  const [form, setForm] = useState({ title: '', body: '', category: 'EDUCATION', pinned: false });
  const [err, setErr] = useState('');

  const refresh = useCallback(async () => {
    try {
      const [d, f] = await Promise.all([api.newsDrafts(), api.newsFeed()]);
      setDrafts(d.articles); setFeed(f.articles); setErr('');
    } catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const act = async (fn: () => Promise<any>) => {
    try { await fn(); setErr(''); await refresh(); }
    catch (e: any) { setErr(e.message); }
  };

  return (
    <>
      <h3>Create article</h3>
      <div style={{ display: 'flex', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
        <input placeholder="Title" value={form.title} onChange={e => setForm({ ...form, title: e.target.value })} style={{ width: 280 }} />
        <select value={form.category} onChange={e => setForm({ ...form, category: e.target.value })}>
          {CATEGORIES.map(c => <option key={c}>{c}</option>)}
        </select>
        <label><input type="checkbox" checked={form.pinned} onChange={e => setForm({ ...form, pinned: e.target.checked })} /> Pinned</label>
      </div>
      <textarea placeholder="Body" value={form.body} onChange={e => setForm({ ...form, body: e.target.value })}
        style={{ width: '100%', minHeight: 80, marginBottom: 6 }} />
      <button disabled={!form.title || !form.body}
        onClick={() => act(() => api.newsCreate(form.title, form.body, form.category, form.pinned))
          .then(() => setForm({ title: '', body: '', category: 'EDUCATION', pinned: false }))}>
        Create draft
      </button>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}

      <h3>Drafts</h3>
      <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
        <thead><tr><Th>Title</Th><Th>Category</Th><Th>Pinned</Th><Th>Created</Th><Th>{''}</Th></tr></thead>
        <tbody>
          {drafts.map(a => (
            <tr key={a.id}>
              <Td>{a.title}</Td><Td>{a.category}</Td><Td>{a.pinned ? '📌' : ''}</Td>
              <Td>{new Date(a.created_at).toLocaleDateString()}</Td>
              <Td><button onClick={() => act(() => api.newsPublish(a.id))}>Publish</button></Td>
            </tr>
          ))}
          {drafts.length === 0 && <tr><Td colSpan={5}>No drafts.</Td></tr>}
        </tbody>
      </table>

      <h3>Published feed</h3>
      <table style={{ borderCollapse: 'collapse', width: '100%' }}>
        <thead><tr><Th>Title</Th><Th>Category</Th><Th>Pinned</Th><Th>Published</Th><Th>{''}</Th></tr></thead>
        <tbody>
          {feed.map(a => (
            <tr key={a.id}>
              <Td>{a.title}</Td><Td>{a.category}</Td><Td>{a.pinned ? '📌' : ''}</Td>
              <Td>{a.published_at ? new Date(a.published_at).toLocaleDateString() : '—'}</Td>
              <Td><button onClick={() => act(() => api.newsArchive(a.id))}>Archive</button></Td>
            </tr>
          ))}
          {feed.length === 0 && <tr><Td colSpan={5}>No published articles.</Td></tr>}
        </tbody>
      </table>
    </>
  );
}

function Disclosures({ api }: { api: Api }) {
  const [list, setList] = useState<Disclosure[]>([]);
  const [form, setForm] = useState({ kind: 'AUDITED_RSA_FUND', title: '', period: '', ref: '', sha256: '' });
  const [err, setErr] = useState('');

  const refresh = useCallback(async () => {
    try { setList((await api.disclosuresList()).disclosures); setErr(''); }
    catch (e: any) { setErr(e.message); }
  }, [api]);

  useEffect(() => { refresh(); }, [refresh]);

  const publish = async () => {
    try {
      await api.disclosurePublish(form.kind, form.title, form.period, form.ref, form.sha256);
      setForm({ kind: 'AUDITED_RSA_FUND', title: '', period: '', ref: '', sha256: '' });
      setErr('');
      await refresh();
    } catch (e: any) {
      // 409 duplicate kind+period — surface the server's message verbatim.
      setErr(e.message);
    }
  };

  return (
    <>
      <p style={{ background: '#fffbeb', border: '1px solid #fcd34d', borderRadius: 6, padding: 8, fontSize: 13 }}>
        ⚠ Disclosures are write-once (WORM): once published, a disclosure for this period cannot be replaced.
      </p>
      <div style={{ display: 'flex', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
        <select value={form.kind} onChange={e => setForm({ ...form, kind: e.target.value })}>
          {KINDS.map(k => <option key={k}>{k}</option>)}
        </select>
        <input placeholder="Title" value={form.title} onChange={e => setForm({ ...form, title: e.target.value })} style={{ width: 220 }} />
        <input placeholder='Period (e.g. "2025" or "2025-Q3")' value={form.period} onChange={e => setForm({ ...form, period: e.target.value })} style={{ width: 180 }} />
        <input placeholder="Reference URL" value={form.ref} onChange={e => setForm({ ...form, ref: e.target.value })} style={{ width: 240 }} />
        <input placeholder="SHA-256" value={form.sha256} onChange={e => setForm({ ...form, sha256: e.target.value })} style={{ width: 260 }} />
        <button disabled={!form.title || !form.period || !form.ref || !form.sha256} onClick={publish}>Publish</button>
      </div>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      <table style={{ borderCollapse: 'collapse', width: '100%' }}>
        <thead><tr><Th>Kind</Th><Th>Title</Th><Th>Period</Th><Th>Ref</Th><Th>SHA-256</Th><Th>Published</Th></tr></thead>
        <tbody>
          {list.map((d, i) => (
            <tr key={`${d.kind}-${d.period}-${i}`}>
              <Td>{d.kind}</Td><Td>{d.title}</Td><Td>{d.period}</Td>
              <Td><a href={d.ref} target="_blank" rel="noreferrer">link</a></Td>
              <Td><span style={{ fontSize: 11, color: '#6b7280' }}>{d.sha256.slice(0, 16)}…</span></Td>
              <Td>{d.published_at ? new Date(d.published_at).toLocaleDateString() : '—'}</Td>
            </tr>
          ))}
          {list.length === 0 && <tr><Td colSpan={6}>No disclosures published.</Td></tr>}
        </tbody>
      </table>
    </>
  );
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children, colSpan }: { children: React.ReactNode; colSpan?: number }) =>
  <td colSpan={colSpan} style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
