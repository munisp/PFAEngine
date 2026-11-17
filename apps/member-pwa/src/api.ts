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

  // ---- Tranche 20: case management + CRM (member self-service) ----
  myCases(memberId: string) {
    return this.req<{ cases: MemberCase[] }>('GET', `/api/v1/members/${encodeURIComponent(memberId)}/cases`);
  }
  raiseCase(memberId: string, type: 'COMPLAINT' | 'SERVICE_REQUEST' | 'GENERAL', subject: string, description: string, channel = 'WEB') {
    return this.req<{ case: MemberCase; merged: boolean }>(
      'POST',
      `/api/v1/members/${encodeURIComponent(memberId)}/cases?channel=${channel}`,
      { type, subject, description },
    );
  }
  rateCase(caseId: string, score: number) {
    return this.req<MemberCase>('POST', `/api/v1/cases/${encodeURIComponent(caseId)}/csat`, { score });
  }

  // ---- Tranche 21: forms, documents, branches, news, disclosures, funds, eKYC ----
  formsList(category?: string) {
    const q = category ? `?category=${encodeURIComponent(category)}` : '';
    return this.req<{ forms: PfaForm[] }>('GET', `/api/v1/forms${q}`);
  }
  myDocuments(memberId: string) {
    return this.req<{ documents: MemberDoc[] }>('GET', `/api/v1/documents/mine?member_id=${encodeURIComponent(memberId)}`);
  }
  submitDocument(input: SubmitDocumentRequest) {
    return this.req<MemberDoc>('POST', '/api/v1/documents', input);
  }
  branchesList(state?: string) {
    const q = state ? `?state=${encodeURIComponent(state)}` : '';
    return this.req<{ branches: Branch[] }>('GET', `/api/v1/branches${q}`);
  }
  branchAvailability(branchId: string, date: string) {
    return this.req<{ slots: number[] }>(
      'GET',
      `/api/v1/branches/${encodeURIComponent(branchId)}/availability?date=${encodeURIComponent(date)}`,
    );
  }
  bookAppointment(input: BookAppointmentRequest) {
    return this.req<Appointment>('POST', '/api/v1/appointments', input);
  }
  myAppointments(memberId: string) {
    return this.req<{ appointments: Appointment[] }>('GET', `/api/v1/appointments?member_id=${encodeURIComponent(memberId)}`);
  }
  newsFeed(category?: string) {
    const q = category ? `?category=${encodeURIComponent(category)}` : '';
    return this.req<{ articles: Article[] }>('GET', `/api/v1/news${q}`);
  }
  disclosuresList() {
    return this.req<{ disclosures: Disclosure[] }>('GET', '/api/v1/disclosures');
  }
  fundPerformance() {
    return this.req<{ performance: Record<string, FundPerf> }>('GET', '/api/v1/funds/performance');
  }
  ekycStart(memberId: string) {
    return this.req<EkycSession>('POST', '/api/v1/ekyc/sessions', { member_id: memberId });
  }
  ekycGet(id: string) {
    return this.req<EkycSession>('GET', `/api/v1/ekyc/sessions/${encodeURIComponent(id)}`);
  }
  ekycSubmitDocument(id: string, input: EkycDocumentSubmission) {
    return this.req<EkycSession>('POST', `/api/v1/ekyc/sessions/${encodeURIComponent(id)}/document`, input);
  }
  ekycVerifyIdentity(id: string, input: EkycIdentityVerification) {
    return this.req<EkycSession>('POST', `/api/v1/ekyc/sessions/${encodeURIComponent(id)}/identity`, input);
  }
  ekycSubmitBiometric(id: string, frames: string[]) {
    return this.req<EkycSession>('POST', `/api/v1/ekyc/sessions/${encodeURIComponent(id)}/biometric`, { frames });
  }
  ekycDecide(id: string) {
    return this.req<EkycSession>('POST', `/api/v1/ekyc/sessions/${encodeURIComponent(id)}/decide`);
  }
}

export interface MemberCase {
  id: string;
  type: string;
  subject: string;
  description: string;
  status: string;
  priority: string;
  source: string;
  created_at: string;
  due_at: string;
  sla_breached: boolean;
  resolution?: string;
  resolution_code?: string;
  csat?: number;
  comments?: { author: string; body: string; at: string }[];
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

// ---- Tranche 21 types (member-svc handler contracts) ----

/** PfaForm is a published, downloadable member form (named PfaForm to avoid
    clashing with DOM naming habits; wire name is "Form"). */
export interface PfaForm {
  id: string;
  code: string;
  title: string;
  category: string;
  version: string;
  description: string;
  ref: string;
  sha256: string;
  active: boolean;
  published_at: string;
}

export type DocKind =
  | 'PASSPORT_PHOTO'
  | 'NIN_SLIP'
  | 'ID_CARD'
  | 'PROOF_OF_ADDRESS'
  | 'BIRTH_CERTIFICATE'
  | 'EMPLOYMENT_LETTER';

export type MemberDocStatus = 'SUBMITTED' | 'UNDER_REVIEW' | 'APPROVED' | 'REJECTED';

export interface MemberDoc {
  id: string;
  member_id: string;
  kind: string;
  filename: string;
  mime: string;
  size: number;
  ref: string;
  sha256: string;
  status: MemberDocStatus;
  review_note: string;
  submitted_at: string;
}

export interface SubmitDocumentRequest {
  member_id: string;
  kind: DocKind;
  filename: string;
  mime: string;
  size: number;
  ref: string;
  sha256: string;
}

export interface Branch {
  id: string;
  name: string;
  address: string;
  city: string;
  state: string;
  phone: string;
  email: string;
  open_hours: string;
  slot_minutes: number;
  slots_per_day: number;
  active: boolean;
}

export type AppointmentStatus = 'BOOKED' | 'CHECKED_IN' | 'DONE' | 'CANCELLED' | 'NO_SHOW';

export interface Appointment {
  id: string;
  branch_id: string;
  member_id: string;
  date: string;
  slot: number;
  purpose: string;
  status: AppointmentStatus;
  booked_at: string;
}

export interface BookAppointmentRequest {
  branch_id: string;
  member_id: string;
  date: string;
  slot: number;
  purpose: string;
}

export type ArticleCategory = 'PENCOM_NOTICE' | 'INDUSTRY' | 'PRODUCT' | 'EDUCATION';

export interface Article {
  id: string;
  title: string;
  body: string;
  category: ArticleCategory;
  pinned: boolean;
  published_at: string;
}

export interface Disclosure {
  id: string;
  kind: string;
  title: string;
  period: string;
  ref: string;
  sha256: string;
  published_at: string;
  published_by: string;
}

export interface FundPerf {
  fund_id: string;
  as_of: string;
  unit_price_kobo: number;
  return_3m_bps?: number;
  return_6m_bps?: number;
  return_12m_bps?: number;
  return_36m_bps?: number;
  ytd_bps?: number;
}

export type EkycDecision = '' | 'AUTO_APPROVE' | 'MANUAL_REVIEW' | 'REJECT';

/** EkycSession is the eKYC pipeline state machine snapshot (named EkycSession
    to avoid clashing with src/session.ts Session). */
export interface EkycSession {
  id: string;
  state: string;
  document: { kind: string; confidence: number; expired: boolean };
  nin_verified: boolean;
  bvn_verified: boolean;
  liveness_score: number;
  liveness_passed: boolean;
  face_score: number;
  face_matched: boolean;
  risk_score: number;
  risk_flags: string[];
  decision: EkycDecision;
  case_id: string;
  created_at: string;
}

export interface EkycDocumentSubmission {
  kind: string;
  ref: string;
  image_b64: string;
  use_vlm: boolean;
}

export interface EkycIdentityVerification {
  nin: string;
  bvn?: string;
  name: string;
  dob: string;
  phone: string;
}
