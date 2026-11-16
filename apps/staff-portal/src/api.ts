// Typed API client for the staff portal — tenant-scoped (X-Tenant-ID, §6).
export interface Tenant { id: string; name: string; license_no: string; deployment: string }
export interface ReconException { id: string; employer_id: string; reason: string; expected_kobo: number; received_kobo: number; rsa_pin: string; raised_at: string }
export interface Claim { id: string; rsa_id: string; type: string; amount_kobo: number; state: string; submitted_by: string }
export interface Transfer { id: string; rsa_id: string; direction: string; value_kobo: number; state: string; quarter: string }

export class Api {
  base: string;
  tenantId: string;
  token?: string;
  constructor(base: string, tenantId: string, token?: string) {
    this.base = base;
    this.tenantId = tenantId;
    this.token = token;
  }
  private headers(): Record<string, string> {
    return { 'Content-Type': 'application/json', 'X-Tenant-ID': this.tenantId, ...(this.token ? { Authorization: `Bearer ${this.token}` } : {}) };
  }
  async req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(this.base + path, { method, headers: this.headers(), body: body ? JSON.stringify(body) : undefined });
    if (!res.ok) throw new Error(`${res.status}: ${await res.text()}`);
    return res.json();
  }
  login(email: string, password: string, totp: string) {
    return this.req<{ token: string }>('POST', '/v1/login', { email, password, totp });
  }
  exceptions() { return this.req<{ exceptions: ReconException[]; aged_buckets: Record<string, number> }>('GET', '/v1/reconciliation/exceptions'); }
  resolveException(id: string, resolution: string, actor: string) {
    return this.req('POST', `/v1/reconciliation/${id}/resolve`, { resolution, actor });
  }
  decideClaim(id: string, approver: string, approve: boolean, reason: string) {
    return this.req<Claim>('POST', `/v1/claims/${id}/decision`, { approver, approve, reason });
  }
  auditExport(approver1: string, approver2: string) {
    return this.req<{ count: number }>('POST', '/v1/audit/export', { approver1, approver2 });
  }

  // ---- Tranche 19: innovation operations (member-svc /api/v1) ----
  fraudCases(openOnly: boolean) {
    return this.req<{ cases: any[] }>('GET', `/api/v1/fraud/cases${openOnly ? '?open=1' : ''}`);
  }
  fraudResolve(caseId: string, status: 'CONFIRMED_FRAUD' | 'CLEARED') {
    return this.req('POST', '/api/v1/fraud/resolve', { case_id: caseId, status });
  }
  agentsLeaderboard() {
    return this.req<{ leaderboard: Record<string, number> }>('GET', '/api/v1/agents/leaderboard');
  }
  regfileList() {
    return this.req<{ filings: any[] }>('GET', '/api/v1/regfile/filings');
  }
  regfileGenerate(id: string, payload: string) {
    return this.req('POST', '/api/v1/regfile/generate', { id, payload });
  }
  readinessReport(employerId: string, inputs: {
    remittances_on_time: number; remittances_total: number;
    employees_covered: number; employees_total: number; avg_contrib_bps: number;
  }) {
    const period = new Date().toISOString().slice(0, 7);
    return this.req<any>('POST', '/api/v1/readiness/reports', { employer_id: employerId, period, inputs });
  }
}

export const naira = (kobo: number) => (kobo / 100).toLocaleString('en-NG', { style: 'currency', currency: 'NGN' });
