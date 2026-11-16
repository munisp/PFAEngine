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
}

/** formatNaira renders integer kobo as ₦ currency (no floats in the API). */
export const formatNaira = (kobo: number): string =>
  new Intl.NumberFormat('en-NG', { style: 'currency', currency: 'NGN' }).format(kobo / 100);
