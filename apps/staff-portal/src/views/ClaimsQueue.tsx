import { useState } from 'react';
import { Api, naira } from '../api';

// Maker-checker claims decisions (FR-401). The API enforces
// approver ≠ submitter; this view is the checker's workbench.
export default function ClaimsQueue({ api }: { api: Api }) {
  const [claimId, setClaimId] = useState('');
  const [approver, setApprover] = useState('');
  const [msg, setMsg] = useState('');

  async function decide(approve: boolean) {
    try {
      const c = await api.decideClaim(claimId, approver, approve, approve ? 'docs verified' : 'insufficient evidence');
      setMsg(`Claim ${c.id} → ${c.state} (${naira(c.amount_kobo)})`);
    } catch (e) {
      setMsg(String(e));
    }
  }

  return (
    <section>
      <h2>Claims maker-checker</h2>
      <input placeholder="Claim ID" value={claimId} onChange={e => setClaimId(e.target.value)} />
      <input placeholder="Approver (≠ submitter)" value={approver} onChange={e => setApprover(e.target.value)} />
      <button onClick={() => decide(true)}>Approve</button>
      <button onClick={() => decide(false)}>Reject</button>
      {msg && <p>{msg}</p>}
    </section>
  );
}
