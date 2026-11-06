// Den branding for Con Services modules. Identical in HR, Stash, Rally and Desk;
// keep the copies in sync.
//
// The backend serves GET <api>/brand: Den's theme when paired, an empty
// theme when standalone (then styles.css defaults apply). Den's theme holds
// hex colours for light mode plus optional overrides under theme.dark; any
// dark colour not overridden is derived from the light palette (darkTheme is
// the same in Den and the Keycloak login theme, so every surface matches).
import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';

export type ModuleBrand = {
  source: 'den' | 'standalone';
  revision: number;
  organization_name: string;
  logo_url?: string;
  login_background_url?: string;
  theme: Record<string, unknown>;
  /** Includes login_layout ("panel" | "centered") and login_caption. */
  copy: Record<string, unknown>;
};

type HSL = [number, number, number];

function hexToHsl(v: unknown): HSL | null {
  if (typeof v !== 'string') return null;
  let h = v.trim().replace(/^#/, '');
  if (h.length === 3) h = h.split('').map((c) => c + c).join('');
  if (!/^[0-9a-f]{6}$/i.test(h)) return null;
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16) / 255);
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;
  let hue = 0;
  let s = 0;
  if (max !== min) {
    const d = max - min;
    s = l > 0.5 ? d / (2 - max - min) : d / (max + min);
    hue = max === r ? (g - b) / d + (g < b ? 6 : 0) : max === g ? (b - r) / d + 2 : (r - g) / d + 4;
    hue *= 60;
  }
  return [Math.round(hue), Math.round(s * 100), Math.round(l * 100)];
}

const css = ([h, s, l]: HSL) => `${h} ${s}% ${l}%`;
const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));

/** Readable text colour on top of an HSL colour. */
function onColor([h, s, l]: HSL): HSL {
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  const a = (s / 100) * Math.min(l / 100, 1 - l / 100);
  const f = (n: number) => {
    const k = (n + h / 30) % 12;
    return l / 100 - a * Math.max(-1, Math.min(k - 3, 9 - k, 1));
  };
  const lum = 0.2126 * lin(f(0)) + 0.7152 * lin(f(8)) + 0.0722 * lin(f(4));
  return lum > 0.45 ? [240, 10, 10] : [0, 0, 100];
}

const THEME_KEYS = ['primary', 'primary_hover', 'accent', 'background', 'muted', 'foreground', 'border'] as const;
type ThemeKey = (typeof THEME_KEYS)[number];

/** Den's dark palette, or null when the theme sets no colours (standalone). */
export type DarkTheme = Record<ThemeKey | 'page', HSL>;

/**
 * Dark colours from Den's theme: theme.dark overrides win, everything else is
 * derived from the light palette. Surfaces are near-black tinted with the
 * brand hue; the primary is lifted so it reads on dark. `background` is the
 * card surface and `page` sits behind it.
 */
export function darkTheme(theme: Record<string, unknown>): DarkTheme | null {
  if (!THEME_KEYS.some((k) => hexToHsl(theme[k]))) return null;
  const over = (theme.dark && typeof theme.dark === 'object' ? theme.dark : {}) as Record<string, unknown>;
  const primary = hexToHsl(theme.primary) ?? [243, 75, 59];
  const bg = hexToHsl(theme.background);
  const [th, tsBase] = bg && bg[1] >= 10 ? bg : primary;
  const ts = clamp(Math.round(tsBase * 0.12), 3, 9);
  const [ph, ps, pl] = primary;
  const accent = hexToHsl(theme.accent) ?? primary;
  const d: DarkTheme = {
    primary: [ph, ps, clamp(pl, 58, 72)],
    primary_hover: [ph, ps, clamp(pl, 58, 72) + 6],
    accent: [accent[0], Math.min(accent[1], 35), 22],
    background: [th, ts, 13],
    page: [th, ts, 10],
    muted: [th, ts + 1, 18],
    foreground: [th, 10, 96],
    border: [th, ts, 22],
  };
  for (const k of THEME_KEYS) {
    const c = hexToHsl(over[k]);
    if (c) d[k] = c;
  }
  const page = hexToHsl(over.page);
  if (page) d.page = page;
  else if (hexToHsl(over.background)) d.page = [d.background[0], d.background[1], clamp(d.background[2] - 3, 0, 100)];
  if (hexToHsl(over.primary) && !hexToHsl(over.primary_hover)) {
    d.primary_hover = [d.primary[0], d.primary[1], clamp(d.primary[2] + 6, 0, 90)];
  }
  return d;
}

/** CSS for :root and :root.dark from Den's theme JSON ("" when nothing applies). */
export function brandCss(theme: Record<string, unknown>): string {
  const light: Record<string, string> = {};
  const dark: Record<string, string> = {};
  const primary = hexToHsl(theme.primary);
  if (primary) {
    const [h, s, l] = primary;
    light['--primary'] = light['--ring'] = css(primary);
    light['--primary-foreground'] = css(onColor(primary));
    light['--primary-hover'] = css(hexToHsl(theme.primary_hover) ?? [h, s, clamp(l - 7, 5, 95)]);
    light['--accent-foreground'] = css([h, clamp(s, 30, 70), 35]);
  }
  const d = darkTheme(theme);
  if (d) {
    const fg = d.foreground;
    Object.assign(dark, {
      '--background': css(d.page),
      '--card': css(d.background),
      '--popover': css(d.background),
      '--muted': css(d.muted),
      '--secondary': css(d.muted),
      '--foreground': css(fg),
      '--card-foreground': css(fg),
      '--popover-foreground': css(fg),
      '--secondary-foreground': css(fg),
      '--muted-foreground': css([fg[0], Math.min(fg[1], 8), clamp(fg[2] - 29, 55, 75)]),
      '--border': css(d.border),
      '--input': css(d.border),
      '--primary': css(d.primary),
      '--ring': css(d.primary),
      '--primary-foreground': css(onColor(d.primary)),
      '--primary-hover': css(d.primary_hover),
      '--primary-soft': css([d.primary[0], Math.min(d.primary[1], 35), 21]),
      '--accent': css(d.accent),
      '--accent-foreground': css([d.accent[0], clamp(d.accent[1] + 40, 30, 85), 82]),
    });
  }
  const map: [string, string[]][] = [
    ['accent', ['--primary-soft', '--accent']],
    ['background', ['--background', '--card', '--popover']],
    ['muted', ['--muted', '--secondary']],
    ['foreground', ['--foreground', '--card-foreground', '--popover-foreground', '--secondary-foreground']],
    ['border', ['--border', '--input']],
  ];
  for (const [key, vars] of map) {
    const c = hexToHsl(theme[key]);
    if (c) for (const v of vars) light[v] = css(c);
  }
  const block = (sel: string, vars: Record<string, string>) =>
    Object.keys(vars).length
      ? `${sel} {\n${Object.entries(vars).map(([k, v]) => `  ${k}: ${v};`).join('\n')}\n}\n`
      : '';
  // :root.dark outranks the module's own .dark block, which loads earlier.
  return block(':root', light) + block(':root.dark', dark);
}

function applyBrand(brand: ModuleBrand | undefined, productName: string | null) {
  let el = document.getElementById('brand') as HTMLStyleElement | null;
  if (!el) {
    el = document.createElement('style');
    el.id = 'brand';
    document.head.appendChild(el);
  }
  el.textContent = brand ? brandCss(brand.theme ?? {}) : '';
  if (productName === null) return;
  const org = brand?.organization_name?.trim();
  document.title = org ? `${productName} · ${org}` : productName;
}

async function fetchBrand(endpoint: string): Promise<ModuleBrand> {
  const res = await fetch(endpoint, { cache: 'no-store', headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`brand: HTTP ${res.status}`);
  return (await res.json()) as ModuleBrand;
}

/**
 * Loads and applies the brand; refetches every minute and on tab focus so a
 * change in Den shows up without a rebuild or reload. Pass productName null
 * when the module sets the document title itself.
 */
export function useModuleBrand(endpoint: string, productName: string | null): ModuleBrand | undefined {
  const q = useQuery({
    queryKey: ['module-brand', endpoint],
    queryFn: () => fetchBrand(endpoint),
    staleTime: 30_000,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    retry: 1,
  });
  useEffect(() => applyBrand(q.data, productName), [q.data, productName]);
  return q.data;
}
