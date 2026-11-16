import { useEffect, useState } from 'react';
import { Api, naira } from '../api';

// Compliance cockpit: regulatory filings (regfile lifecycle with late
// penalties) + employer retirement-readiness index (readiness engine).
export default function Compliance({ api }: { api: Api }) {
  const [filings, setFilings] = useState<any[]>([]);
  const [msg, setMsg] = useState('');
  const [readiness, setReadiness] = useState<any>(null);
  const [employerId, setEmployerId] = useState('');
  const [onTime, setOnTime] = useState('18');
  const [total, setTotal] = useState('20');
  const [covered, setCovered] = useState('45');
  const [employees, setEmployees] = useState('50');
  const [avgBps, setAvgBps] = useState('1800');

  function loadFilings() {
    api.regfileList().then(r => setFilings(r.filings ?? [])).catch(e => setMsg(String(e)));
  }
  useEffect(loadFilings, []);

  async function genFiling(id: string) {
    try {
      await api.regfileGenerate(id, '{}');
      setMsg(`Filing ${id} generated`);
      loadFilings();
    } catch (e) { setMsg(String(e)); }
  }

  async function readinessReport() {
    try {
      const r = await api.readinessReport(employerId, {
        remittances_on_time: Number(onTime),
        remittances_total: Number(total),
        employees_covered: Number(covered),
        employees_total: Number(employees),
        avg_contrib_bps: Number(avgBps),
      });
      setReadiness(r);
    } catch (e) { setMsg(String(e)); }
  }

  return (
    <section>
      <h2>Compliance</h2>
      <h3>Regulatory filings</h3>
      <button onClick={loadFilings}>Refresh list</button>
      {filings.length > 0 && (
        <table style={{ width: '100%', marginTop: 8 }}>
          <thead><tr><th>ID</th><th>Kind</th><th>Period</th><th>Status</th><th>Penalty</th><th></th></tr></thead>
          <tbody>
            {filings.map((f: any) => (
              <tr key={f.id}>
                <td>{f.id}</td><td>{f.kind}</td><td>{f.period}</td>
                <td><strong>{f.status}</strong></td>
                <td>{f.penalty_kobo ? naira(f.penalty_kobo) : '—'}</td>
                <td>{f.status === 'PENDING' || f.status === 'LATE'
                  ? <button onClick={() => genFiling(f.id)}>Generate</button>
                  : null}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <h3 style={{ marginTop: 24 }}>Employer readiness index</h3>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <input placeholder="Employer ID" value={employerId} onChange={e => setEmployerId(e.target.value)} />
        <input placeholder="On-time" value={onTime} onChange={e => setOnTime(e.target.value)} style={{ width: 80 }} />
        <input placeholder="Total" value={total} onChange={e => setTotal(e.target.value)} style={{ width: 70 }} />
        <input placeholder="Covered" value={covered} onChange={e => setCovered(e.target.value)} style={{ width: 80 }} />
        <input placeholder="Employees" value={employees} onChange={e => setEmployees(e.target.value)} style={{ width: 90 }} />
        <input placeholder="Avg bps" value={avgBps} onChange={e => setAvgBps(e.target.value)} style={{ width: 80 }} />
        <button onClick={readinessReport}>Compute</button>
      </div>
      {readiness && (
        <p>Index: <strong>{readiness.index}</strong>/100 — grade <strong>{readiness.grade}</strong>{' '}
          (timeliness {readiness.timeliness}, coverage {readiness.coverage}, adequacy {readiness.adequacy})</p>
      )}
      {msg && <p>{msg}</p>}
    </section>
  );
}
