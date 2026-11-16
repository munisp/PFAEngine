// Per-tenant theming (PLT-14): CSS custom properties loaded from
// /theme.json at startup, falling back to the warm default palette.

export interface Theme {
  bg: string;
  surface: string;
  ink: string;
  muted: string;
  accent: string;
  'accent-soft': string;
  border: string;
  ok: string;
  warn: string;
}

interface ThemeFile {
  default?: Partial<Theme>;
  tenants?: Record<string, Partial<Theme>>;
}

export const DEFAULT_THEME: Theme = {
  bg: '#f7f4ee',
  surface: '#ffffff',
  ink: '#2e2a24',
  muted: '#8a8177',
  accent: '#a65a3a',
  'accent-soft': '#f3e9e0',
  border: '#e7e0d5',
  ok: '#5e7b5a',
  warn: '#a85b32',
};

export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  for (const [key, value] of Object.entries(theme)) {
    root.style.setProperty(`--${key}`, value);
  }
}

/** loadTheme fetches /theme.json (optional) and merges tenant overrides. */
export async function loadTheme(tenantId: string): Promise<Theme> {
  let file: ThemeFile = {};
  try {
    const res = await fetch('/theme.json');
    if (res.ok) file = (await res.json()) as ThemeFile;
  } catch {
    // Offline or missing theme — fall through to defaults.
  }
  const merged: Theme = {
    ...DEFAULT_THEME,
    ...(file.default ?? {}),
    ...((file.tenants ?? {})[tenantId] ?? {}),
  };
  applyTheme(merged);
  return merged;
}
