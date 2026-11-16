import { useState } from 'react';
import { Api } from './api';
import ReconQueue from './views/ReconQueue';
import ClaimsQueue from './views/ClaimsQueue';
import AuditExport from './views/AuditExport';
import FraudCases from './views/FraudCases';
import Agents from './views/Agents';
import Compliance from './views/Compliance';

// Staff operations portal: reconciliation exceptions, claims maker-checker,
// audit export (dual control) — the compliance cockpit (BR-09, FR-502).
export default function App() {
  const [tenantId, setTenantId] = useState('demo-pfa');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [totp, setTotp] = useState('');
  const [token, setToken] = useState<string>();
  const [view, setView] = useState<'recon' | 'claims' | 'audit' | 'fraud' | 'agents' | 'compliance'>('recon');
  const [error, setError] = useState('');

  const api = new Api(import.meta.env.VITE_API_BASE ?? 'http://localhost:8080', tenantId, token);

  async function doLogin() {
    try {
      const r = await api.login(email, password, totp);
      setToken(r.token);
      setError('');
    } catch (e) {
      setError(String(e));
    }
  }

  if (!token) {
    return (
      <main style={{ maxWidth: 360, margin: '10vh auto', fontFamily: 'system-ui' }}>
        <h1>PFAEngine Staff</h1>
        <p>Tenant</p>
        <input value={tenantId} onChange={e => setTenantId(e.target.value)} style={{ width: '100%' }} />
        <p>Email</p>
        <input value={email} onChange={e => setEmail(e.target.value)} style={{ width: '100%' }} />
        <p>Password</p>
        <input type="password" value={password} onChange={e => setPassword(e.target.value)} style={{ width: '100%' }} />
        <p>TOTP (if enrolled)</p>
        <input value={totp} onChange={e => setTotp(e.target.value)} style={{ width: '100%' }} />
        <button onClick={doLogin} style={{ marginTop: 16, width: '100%' }}>Sign in</button>
        {error && <p style={{ color: 'crimson' }}>{error}</p>}
      </main>
    );
  }

  return (
    <main style={{ maxWidth: 960, margin: '24px auto', fontFamily: 'system-ui' }}>
      <header style={{ display: 'flex', gap: 16, alignItems: 'baseline' }}>
        <h1>PFAEngine Ops — {tenantId}</h1>
        <nav style={{ display: 'flex', gap: 8 }}>
          <button onClick={() => setView('recon')}>Reconciliation</button>
          <button onClick={() => setView('claims')}>Claims</button>
          <button onClick={() => setView('audit')}>Audit export</button>
          <button onClick={() => setView('fraud')}>Fraud cases</button>
          <button onClick={() => setView('agents')}>Agents</button>
          <button onClick={() => setView('compliance')}>Compliance</button>
        </nav>
      </header>
      {view === 'recon' && <ReconQueue api={api} />}
      {view === 'claims' && <ClaimsQueue api={api} />}
      {view === 'audit' && <AuditExport api={api} />}
      {view === 'fraud' && <FraudCases api={api} />}
      {view === 'agents' && <Agents api={api} />}
      {view === 'compliance' && <Compliance api={api} />}
    </main>
  );
}
