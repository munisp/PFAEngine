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

  // ---- Tranche 20: unified case management + CRM ----
  casesList(q: { status?: string; type?: string; queue?: string; open?: boolean; breached?: boolean; member_id?: string }) {
    const p = new URLSearchParams();
    if (q.status) p.set('status', q.status);
    if (q.type) p.set('type', q.type);
    if (q.queue) p.set('queue', q.queue);
    if (q.member_id) p.set('member_id', q.member_id);
    if (q.open) p.set('open', '1');
    if (q.breached) p.set('breached', '1');
    const s = p.toString();
    return this.req<{ cases: CaseRow[] }>('GET', '/api/v1/cases' + (s ? `?${s}` : ''));
  }
  caseGet(id: string) { return this.req<CaseRow>('GET', `/api/v1/cases/${id}`); }
  caseTransition(id: string, status: string, note: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/transition`, { status, note });
  }
  caseAssign(id: string, assignee: string, queue?: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/assign`, { assignee, queue });
  }
  caseComment(id: string, body: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/comments`, { body });
  }
  caseAddTask(id: string, title: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/tasks`, { title });
  }
  caseCompleteTask(id: string, taskId: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/tasks/${taskId}/complete`, {});
  }
  caseResolve(id: string, resolution: string, code: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/resolve`, { resolution, code });
  }
  caseEscalate(id: string, reason: string) {
    return this.req<CaseRow>('POST', `/api/v1/cases/${id}/escalate`, { reason });
  }
  caseSweep() { return this.req<{ breached: CaseRow[]; count: number }>('POST', '/api/v1/cases/sweep', {}); }
  caseStats() { return this.req<CaseStats>('GET', '/api/v1/cases/stats'); }
  crmMember360(memberId: string) { return this.req<Member360>('GET', `/api/v1/crm/members/${memberId}/360`); }
  crmTimeline(memberId: string) {
    return this.req<{ timeline: Interaction[] }>('GET', `/api/v1/crm/members/${memberId}/timeline`);
  }
  crmSegmentMembers(tag: string) {
    return this.req<{ segment: string; members: { member_id: string; name: string; segments: string[] }[] }>('GET', `/api/v1/crm/segments/${tag}/members`);
  }
  crmCampaigns() { return this.req<{ campaigns: Campaign[] }>('GET', '/api/v1/crm/campaigns'); }
  crmCreateCampaign(name: string, segment: string, channel: string, message: string) {
    return this.req<Campaign>('POST', '/api/v1/crm/campaigns', { name, segment, channel, message });
  }
  crmLaunchCampaign(id: string) {
    return this.req<Campaign>('POST', `/api/v1/crm/campaigns/${id}/launch`, {});
  }
  crmComplaintsRegister(from: string, to: string) {
    return this.req<{ register: ComplaintRow[] }>('GET', `/api/v1/crm/complaints/register?from=${from}&to=${to}`);
  }
}

export interface CaseComment { author: string; body: string; at: string }
export interface CaseTask { id: string; title: string; done: boolean; done_by?: string }
export interface CaseEvent { at: string; actor: string; action: string; detail?: string }
export interface CaseRow {
  id: string; type: string; subject: string; description: string;
  member_id?: string; employer_id?: string; linked_kind?: string; linked_id?: string;
  source: string; status: string; priority: string; queue: string; assignee?: string;
  due_at: string; sla_breached: boolean; created_at: string; updated_at: string;
  resolved_at?: string; resolution?: string; resolution_code?: string; csat?: number;
  links?: string[]; comments?: CaseComment[]; tasks?: CaseTask[]; events?: CaseEvent[];
}
export interface CaseStats {
  total: number; open: number; breached: number;
  by_status: Record<string, number>; by_type: Record<string, number>; by_queue: Record<string, number>;
  avg_resolve_hours: number; csat_avg: number; csat_count: number;
  open_by_age_days: Record<string, number>;
}
export interface Interaction {
  id: string; member_id: string; channel: string; direction: string;
  kind: string; summary: string; case_id?: string; staff_id?: string; at: string;
}
export interface Member360 {
  member_id: string;
  facts: { member_id: string; joined_at: string; age_years: number; balance_kobo: number; micro_pension: boolean; diaspora: boolean };
  segments: string[];
  open_cases: CaseRow[]; recent_cases: CaseRow[];
  timeline: Interaction[];
  csat_avg: number; complaints_total: number; complaints_upheld: number;
}
export interface Campaign {
  id: string; name: string; segment: string; channel: string; message: string;
  status: string; recipients: number; created_at: string; launched_at?: string;
}
export interface ComplaintRow {
  case_id: string; member_id: string; subject: string; received_at: string;
  resolved_at?: string; status: string; outcome?: string; days_to_resolve: number; csat?: number;
}

export const naira = (kobo: number) => (kobo / 100).toLocaleString('en-NG', { style: 'currency', currency: 'NGN' });
