// Session persistence for the member PWA (localStorage-backed).

export interface Session {
  memberId: string;
  name: string;
  phone: string;
  tenantId: string;
}

const SESSION_KEY = 'pfa.session';

export function loadSession(): Session | null {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    return raw ? (JSON.parse(raw) as Session) : null;
  } catch {
    return null;
  }
}

export function saveSession(session: Session): void {
  localStorage.setItem(SESSION_KEY, JSON.stringify(session));
}

export function clearSession(): void {
  localStorage.removeItem(SESSION_KEY);
}
