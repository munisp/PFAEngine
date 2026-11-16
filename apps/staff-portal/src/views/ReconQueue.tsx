import { useEffect, useState } from 'react';
import { Api, naira } from '../api';
import type { ReconException } from '../api';

// Reconciliation exception queue with aging buckets (FR-303, §4).
export default function ReconQueue({ api }: { api: Api }) {
  const [data, setData] = useState<{ exceptions: ReconException[]; aged_buckets: Record<string, number> }>();
  const [msg, setMsg] = useState('');
  const load = () => api.exceptions().then(setData).catch(e => setMsg(String(e)));
  useEffect(() => { load(); }, []);

  async function resolve(id: string, resolution: string) {
    await api.resolveException(id, resolution, 'ops-staff');
    load();
  }

  return (
    <section>
      <h2>Exceptions ({data?.exceptions?.length ?? 0})</h2>
      <p>Aging: {data && Object.entries(data.aged_buckets).map(([k, v]) => `${k}d: ${v}`).join(' · ')}</p>
      <table style={{ width: '100%', borderCollapse: 'collapse' }}>
        <thead><tr><th>PIN</th><th>Reason</th><th>Expected</th><th>Received</th><th></th></tr></thead>
        <tbody>
          {data?.exceptions?.map(e => (
            <tr key={e.id}>
              <td>{e.rsa_pin}</td><td>{e.reason}</td>
              <td>{naira(e.expected_kobo)}</td><td>{naira(e.received_kobo)}</td>
              <td>
                <button onClick={() => resolve(e.id, 'RECLASSIFY')}>Reclassify</button>
                <button onClick={() => resolve(e.id, 'REFUND')}>Refund</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {msg && <p style={{ color: 'crimson' }}>{msg}</p>}
    </section>
  );
}
