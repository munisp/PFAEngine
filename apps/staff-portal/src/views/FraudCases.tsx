import { useEffect, useState } from 'react';
import { Api } from '../api';

// Fraud case workbench: ML + rules flagged transactions queue for
// investigators (fraud engine, internal/fraud; served by member-svc).
export default function FraudCases({ api }: { api: Api }) {
  const [cases, setCases] = useState<any[]>([]);
  const [msg, setMsg] = useState('');

  function load() {
    api.fraudCases(true).then(r => setCases(r.cases ?? [])).catch(e => setMsg(String(e)));
  }
  useEffect(load, []);

  async function resolve(id: string, status: 'CONFIRMED_FRAUD' | 'CLEARED') {
    try {
      await api.fraudResolve(id, status);
      setMsg(`Case ${id} → ${status}`);
      load();
    } catch (e) { setMsg(String(e)); }
  }

  return (
    <section>
      <h2>Fraud case queue</h2>
      <button onClick={load}>Refresh</button>
      {cases.length === 0 && <p>No open cases.</p>}
      <table style={{ width: '100%', marginTop: 12 }}>
        <thead><tr><th>Case</th><th>Member</th><th>Score</th><th>Signals</th><th></th></tr></thead>
        <tbody>
          {cases.map((c: any) => (
            <tr key={c.id}>
              <td>{c.id}</td>
              <td>{c.member_id}</td>
              <td><strong>{c.score}</strong></td>
              <td>{(c.signals ?? []).join(', ')}</td>
              <td>
                <button onClick={() => resolve(c.id, 'CONFIRMED_FRAUD')}>Confirm fraud</button>
                <button onClick={() => resolve(c.id, 'CLEARED')}>Clear</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {msg && <p>{msg}</p>}
    </section>
  );
}
