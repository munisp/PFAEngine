import { useEffect, useState } from 'react';
import { Api, naira } from '../api';
import type { Campaign, Member360 } from '../api';

// CRM — member 360 single-pane view, segment explorer, campaign manager
// and the PenCom quarterly complaints register (Tranche 20).
export default function CRM({ api }: { api: Api }) {
  const [memberId, setMemberId] = useState('');
  const [v, setV] = useState<Member360 | null>(null);
  const [err, setErr] = useState('');
  const [campaigns, setCampaigns] = useState<Campaign[]>([]);
  const [cmp, setCmp] = useState({ name: '', segment: 'DORMANT', channel: 'SMS', message: '' });
  const [register, setRegister] = useState<any[] | null>(null);

  useEffect(() => { api.crmCampaigns().then(r => setCampaigns(r.campaigns)).catch(() => {}); }, [api]);

  const load360 = async () => {
    try { setV(await api.crmMember360(memberId)); setErr(''); }
    catch (e: any) { setV(null); setErr(e.message); }
  };
  const createCampaign = async () => {
    try {
      await api.crmCreateCampaign(cmp.name, cmp.segment, cmp.channel, cmp.message);
      setCampaigns((await api.crmCampaigns()).campaigns);
      setCmp({ name: '', segment: 'DORMANT', channel: 'SMS', message: '' });
      setErr('');
    } catch (e: any) { setErr(e.message); }
  };
  const launch = async (id: string) => {
    try {
      await api.crmLaunchCampaign(id);
      setCampaigns((await api.crmCampaigns()).campaigns);
      setErr('');
    } catch (e: any) { setErr(e.message); }
  };
  const loadRegister = async () => {
    try {
      const to = new Date().toISOString().slice(0, 10);
      const from = new Date(Date.now() - 90 * 86400_000).toISOString().slice(0, 10);
      setRegister((await api.crmComplaintsRegister(from, to)).register);
    } catch (e: any) { setErr(e.message); }
  };

  return (
    <section>
      <h2>CRM — member 360</h2>
      <div style={{ display: 'flex', gap: 8, marginBottom: 12 }}>
        <input placeholder="Member ID" value={memberId} onChange={e => setMemberId(e.target.value)} style={{ width: 220 }} />
        <button onClick={load360} disabled={!memberId}>Load 360</button>
      </div>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}
      {v && (
        <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: 12, marginBottom: 16 }}>
          <h3 style={{ marginTop: 0 }}>{v.member_id}</h3>
          <p style={{ fontSize: 13 }}>
            Age {v.facts.age_years} · Balance {naira(v.facts.balance_kobo)} · Joined {new Date(v.facts.joined_at).toLocaleDateString()}
            {v.facts.micro_pension && ' · Micro-pension'}{v.facts.diaspora && ' · Diaspora'}
          </p>
          <p style={{ fontSize: 13 }}>
            Segments: {v.segments.map(s => <span key={s} style={{ background: '#eef2ff', borderRadius: 4, padding: '2px 6px', marginRight: 4 }}>{s}</span>)}
          </p>
          <p style={{ fontSize: 13 }}>
            CSAT {v.csat_avg ? v.csat_avg.toFixed(1) + '/5' : '—'} · Complaints {v.complaints_total} (upheld {v.complaints_upheld}) · Open cases {v.open_cases?.length ?? 0}
          </p>
          <h4>Timeline</h4>
          <ul style={{ paddingLeft: 16, maxHeight: 180, overflow: 'auto' }}>
            {(v.timeline || []).map(ix => (
              <li key={ix.id} style={{ fontSize: 13 }}>
                {new Date(ix.at).toLocaleString()} — <b>{ix.channel}</b> {ix.direction === 'OUTBOUND' ? '→' : '←'} {ix.summary}
                {ix.case_id && <span style={{ color: '#6b7280' }}> ({ix.case_id})</span>}
              </li>
            ))}
            {(v.timeline || []).length === 0 && <li style={{ fontSize: 13 }}>No interactions recorded yet.</li>}
          </ul>
        </div>
      )}

      <h2>Campaigns</h2>
      <div style={{ display: 'flex', gap: 6, marginBottom: 8, flexWrap: 'wrap' }}>
        <input placeholder="Campaign name" value={cmp.name} onChange={e => setCmp({ ...cmp, name: e.target.value })} />
        <select value={cmp.segment} onChange={e => setCmp({ ...cmp, segment: e.target.value })}>
          {['DORMANT', 'HIGH_VALUE', 'MICRO_PENSION', 'DIASPORA', 'PRE_RETIREMENT', 'NEW_MEMBER', 'AT_RISK', 'ACTIVE'].map(s => <option key={s}>{s}</option>)}
        </select>
        <select value={cmp.channel} onChange={e => setCmp({ ...cmp, channel: e.target.value })}>
          {['SMS', 'WHATSAPP', 'EMAIL', 'USSD'].map(c => <option key={c}>{c}</option>)}
        </select>
        <input placeholder="Message" value={cmp.message} onChange={e => setCmp({ ...cmp, message: e.target.value })} style={{ width: 260 }} />
        <button onClick={createCampaign} disabled={!cmp.name || !cmp.message}>Draft</button>
      </div>
      <table style={{ borderCollapse: 'collapse', marginBottom: 16 }}>
        <thead><tr><th style={th}>Name</th><th style={th}>Segment</th><th style={th}>Channel</th><th style={th}>Status</th><th style={th}>Recipients</th><th style={th}></th></tr></thead>
        <tbody>
          {campaigns.map(c => (
            <tr key={c.id}>
              <td style={td}>{c.name}</td><td style={td}>{c.segment}</td><td style={td}>{c.channel}</td>
              <td style={td}>{c.status}</td><td style={td}>{c.recipients}</td>
              <td style={td}>{c.status === 'DRAFT' && <button onClick={() => launch(c.id)}>Launch</button>}</td>
            </tr>
          ))}
          {campaigns.length === 0 && <tr><td style={td} colSpan={6}>No campaigns yet.</td></tr>}
        </tbody>
      </table>

      <h2>PenCom complaints register (last quarter)</h2>
      <button onClick={loadRegister}>Generate register</button>
      {register && (
        <table style={{ borderCollapse: 'collapse', marginTop: 8 }}>
          <thead><tr><th style={th}>Case</th><th style={th}>Member</th><th style={th}>Subject</th><th style={th}>Received</th><th style={th}>Status</th><th style={th}>Outcome</th><th style={th}>Days</th></tr></thead>
          <tbody>
            {register.map(r => (
              <tr key={r.case_id}>
                <td style={td}>{r.case_id}</td><td style={td}>{r.member_id}</td><td style={td}>{r.subject}</td>
                <td style={td}>{new Date(r.received_at).toLocaleDateString()}</td>
                <td style={td}>{r.status}</td><td style={td}>{r.outcome || '—'}</td><td style={td}>{r.days_to_resolve}</td>
              </tr>
            ))}
            {register.length === 0 && <tr><td style={td} colSpan={7}>No complaints in period.</td></tr>}
          </tbody>
        </table>
      )}
    </section>
  );
}

const th: React.CSSProperties = { textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 };
const td: React.CSSProperties = { borderBottom: '1px solid #f3f4f6', padding: 6 };
