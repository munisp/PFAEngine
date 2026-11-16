import { useState } from 'react';
import { Api } from '../api';

// PenCom inspection export under dual control (SC-02, §6.3).
export default function AuditExport({ api }: { api: Api }) {
  const [a1, setA1] = useState('');
  const [a2, setA2] = useState('');
  const [msg, setMsg] = useState('');

  async function doExport() {
    try {
      const r = await api.auditExport(a1, a2);
      setMsg(`Exported ${r.count} hash-chained events`);
    } catch (e) {
      setMsg(String(e));
    }
  }

  return (
    <section>
      <h2>Audit export (dual control)</h2>
      <input placeholder="Approver 1" value={a1} onChange={e => setA1(e.target.value)} />
      <input placeholder="Approver 2" value={a2} onChange={e => setA2(e.target.value)} />
      <button onClick={doExport}>Export for PenCom</button>
      {msg && <p>{msg}</p>}
    </section>
  );
}
