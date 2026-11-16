// Typed API client for the member PWA — tenant-scoped (X-Tenant-ID, §6),
// talking to cmd/member-svc (/api/v1/*).

export interface OnboardRequest {
  full_name: string;
  dob: string;
  phone: string;
  email: string;
  nin: string;
  bvn?: string;
}

export interface OnboardResponse {
  member_id: string;
  full_name: string;
  nin_verified: boolean;
  bvn_verified: boolean;
  duplicate_of?: string;
}

export interface BalanceResponse {
  member_id: string;
  balance_kobo: number;
  balance_naira: number;
}

export interface StatementTxn {
  date: string;
  description: string;
  amount_kobo: number;
  balance_kobo: number;
}

export interface StatementHolding {
  fund_id: string;
  units: number;
}

export interface StatementPayload {
  id: string;
  tenant_id: string;
  rsa_id: string;
  member_name: string;
  rsa_pin: string;
  period: string;
  opening_kobo: number;
  closing_kobo: number;
  holdings: StatementHolding[] | null;
  transactions: StatementTxn[] | null;
  verify_code: string;
  generated_at: string;
}

export interface VerifyRequest {
  rsa_id: string;
  period: string;
  closing_kobo: number;
  code: string;
}

export interface VerifyResponse {
  valid: boolean;
}

// AliveProof is the I-Am-Alive liveness submission (stub endpoint until the
// benefit service ships the member-facing route; the view queues offline).
export interface AliveProof {
  proof: 'selfie' | 'file';
  captured_at: string;
  image_data_url?: string;
}

/** ApiResult carries the payload plus whether the SW served it from cache. */
export interface ApiResult<T> {
  data: T;
  fromCache: boolean;
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export class Api {
  base: string;
  tenantId: string;
  constructor(base: string, tenantId: string) {
    this.base = base;
    this.tenantId = tenantId;
  }
  private headers(): Record<string, string> {
    return { 'Content-Type': 'application/json', 'X-Tenant-ID': this.tenantId };
  }
  async req<T>(method: string, path: string, body?: unknown): Promise<ApiResult<T>> {
    const res = await fetch(this.base + path, {
      method,
      headers: this.headers(),
      body: body ? JSON.stringify(body) : undefined,
    });
    const fromCache = res.headers.get('x-pwa-cache') === 'hit';
    if (!res.ok) throw new ApiError(res.status, await res.text());
    const data = (await res.json()) as T;
    return { data, fromCache };
  }
  onboard(input: OnboardRequest) {
    return this.req<OnboardResponse>('POST', '/api/v1/members', input);
  }
  balance(memberId: string) {
    return this.req<BalanceResponse>('GET', `/api/v1/members/${memberId}/balance`);
  }
  statement(memberId: string, period?: string) {
    const q = period ? `?period=${encodeURIComponent(period)}` : '';
    return this.req<StatementPayload>('GET', `/api/v1/members/${memberId}/statement${q}`);
  }
  verifyStatement(input: VerifyRequest) {
    return this.req<VerifyResponse>('POST', '/api/v1/statements/verify', input);
  }
  submitAliveProof(memberId: string, proof: AliveProof) {
    return this.req<{ accepted: boolean }>('POST', `/api/v1/members/${memberId}/alive`, proof);
  }

  // ---- Tranche 19: innovation engines ----
  advisoryProject(input: { balance_kobo: number; monthly_contrib_kobo: number; years_to_retire: number; annual_return_bps: number; salary_kobo: number; replacement_bps: number }) {
    return this.req<AdvisoryProjection>('POST', '/api/v1/advisory/project', input);
  }
  escalationRules(memberId: string) {
    return this.req<{ rules: EscalationRule[] }>('GET', `/api/v1/escalation/rules?member_id=${encodeURIComponent(memberId)}`);
  }
  escalationEnrol(input: { member_id: string; base_rate_bps: number; step_bps: number; max_bps: number }) {
    return this.req<EscalationRule>('POST', '/api/v1/escalation/rules', input);
  }
  escalationOptOut(input: { member_id: string; reason: string }) {
    return this.req<{ ok: boolean }>('POST', '/api/v1/escalation/optout', input);
  }
  roundupWallet(memberId: string) {
    return this.req<{ wallet: RoundupWallet; sweeps: unknown[] }>('GET', `/api/v1/roundup/wallets?member_id=${encodeURIComponent(memberId)}`);
  }
  roundupOpen(input: { member_id: string; step_kobo: number; monthly_cap_kobo: number }) {
    return this.req<RoundupWallet>('POST', '/api/v1/roundup/wallets', input);
  }
  glidepathGet(memberId: string) {
    return this.req<{ member: GlidepathMember; switches: GlidepathSwitch[] }>('GET', `/api/v1/glidepath/members?member_id=${encodeURIComponent(memberId)}`);
  }
  glidepathRegister(input: { member_id: string; age_years: number; retired: boolean; aggressive: boolean }) {
    return this.req<GlidepathMember>('POST', '/api/v1/glidepath/members', input);
  }
  glidepathElect(input: { member_id: string; fund: string }) {
    return this.req<GlidepathSwitch>('POST', '/api/v1/glidepath/elect', input);
  }
  mortgageApply(input: { member_id: string; rsa_balance_kobo: number; amount_kobo: number; lender_ref: string; employed: boolean }) {
    return this.req<MortgageApplication>('POST', '/api/v1/mortgage/applications', input);
  }
  mortgageList(memberId: string) {
    return this.req<{ applications: MortgageApplication[] }>('GET', `/api/v1/mortgage/applications?member_id=${encodeURIComponent(memberId)}`);
  }
  claimsOpen(rsaId: string) {
    return this.req<ClaimsPack>('POST', '/api/v1/claimsfast/packs', { rsa_id: rsaId });
  }
  claimsComplete(rsaId: string, item: string) {
    return this.req<ClaimsPack>('POST', '/api/v1/claimsfast/complete', { rsa_id: rsaId, item });
  }
  successionGet(memberId: string) {
    return this.req<Nomination>('GET', `/api/v1/succession/nominations?member_id=${encodeURIComponent(memberId)}`);
  }
  successionNominate(input: { member_id: string; beneficiaries: Beneficiary[] }) {
    return this.req<Nomination>('POST', '/api/v1/succession/nominations', input);
  }
  literacyCatalogue(lang: string) {
    return this.req<{ lessons: Lesson[] }>('GET', `/api/v1/literacy/catalogue?lang=${encodeURIComponent(lang)}`);
  }
  literacyAnswer(input: { member_id: string; lesson_id: string; option: number }) {
    return this.req<{ passed: boolean }>('POST', '/api/v1/literacy/answer', input);
  }
  literacyScore(memberId: string) {
    return this.req<{ passed: number; total: number }>('GET', `/api/v1/literacy/score?member_id=${encodeURIComponent(memberId)}`);
  }
  diasporaContribute(input: { rsa_id: string; currency: string; amount_minor: number }) {
    return this.req<DiasporaContribution>('POST', '/api/v1/diaspora/contributions', input);
  }
  diasporaList(rsaId: string) {
    return this.req<{ contributions: DiasporaContribution[]; total_kobo: number }>('GET', `/api/v1/diaspora/contributions?rsa_id=${encodeURIComponent(rsaId)}`);
  }
}

/** formatNaira renders integer kobo as ₦ currency (no floats in the API). */
export const formatNaira = (kobo: number): string =>
  new Intl.NumberFormat('en-NG', { style: 'currency', currency: 'NGN' }).format(kobo / 100);

// ---- Tranche 19 types (exact Go handler contracts) ----
export interface AdvisoryProjection {
  balance_now_kobo: number;
  monthly_contrib_kobo: number;
  years_to_retire: number;
  annual_return_bps: number;
  projected_balance_kobo: number;
  expected_pension_kobo: number;
  target_pension_kobo: number;
  gap_kobo: number;
  extra_monthly_kobo: number;
  on_track: boolean;
}
export interface EscalationRule {
  id: string;
  member_id: string;
  base_rate_bps: number;
  step_bps: number;
  max_bps: number;
  current_bps: number;
  opted_out: boolean;
}
export interface RoundupWallet {
  id: string;
  member_id: string;
  step_kobo: number;
  monthly_cap_kobo: number;
  swept_kobo: number;
  swept_this_month_kobo: number;
}
export interface GlidepathMember {
  member_id: string;
  age_years: number;
  retired: boolean;
  fund: string; // "I" | "II" | "III" | "IV"
  aggressive: boolean;
}
export interface GlidepathSwitch { id: string; from: string; to: string; at: string }
export interface MortgageApplication {
  id: string;
  member_id: string;
  rsa_balance_kobo: number;
  amount_kobo: number;
  lender_ref: string;
  status: string;
  reason?: string;
  events: string[];
}
export interface ClaimsPack {
  id: string;
  rsa_id: string;
  items: { label: string; done: boolean }[];
  score: number; // 0-100; 100 = straight-through
  stp: boolean;
  missing: string[];
}
export interface Beneficiary { name: string; relationship: string; phone: string; share_bps: number }
export interface Nomination {
  id: string;
  member_id: string;
  version: number;
  beneficiaries: Beneficiary[];
  next_reconfirm_due: string;
  superseded: boolean;
}
export interface Lesson {
  id: string;
  lang: string;
  title: string;
  body: string;
  question: string;
  options: string[];
}
export interface DiasporaContribution {
  id: string;
  rsa_id: string;
  currency: string;
  credited_kobo: number;
}
