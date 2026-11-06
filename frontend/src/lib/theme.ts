// Light/dark preference shared by every Con Services app. Identical in Den,
// HR, Stash, Rally and Desk; keep the copies in sync.
//
// Den stores the choice per person (modules read and write it through their
// Den link), so picking dark in one app applies everywhere. localStorage only
// caches it so the first paint is right; public/index.html reads the same key.
import { useCallback, useEffect, useSyncExternalStore } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';

export type ThemePreference = 'system' | 'light' | 'dark';

export type Preferences = {
  theme: ThemePreference;
  /** Null until the person first picks a theme in any app. */
  updated_at?: string | null;
  /** False when the module is standalone or Den is unreachable. */
  synced?: boolean;
};

export const THEME_CACHE_KEY = 'cs-theme';
const LEGACY_KEYS = ['stash-theme', 'hr-theme', 'rally-theme'];

const isPreference = (v: unknown): v is ThemePreference => v === 'system' || v === 'light' || v === 'dark';
const darkQuery = () => window.matchMedia('(prefers-color-scheme: dark)');

function readCache(): ThemePreference {
  try {
    const v = localStorage.getItem(THEME_CACHE_KEY);
    if (isPreference(v)) return v;
    for (const k of LEGACY_KEYS) {
      const old = localStorage.getItem(k);
      if (old === 'light' || old === 'dark') return old;
    }
  } catch {
    /* private mode */
  }
  return 'system';
}

let current: ThemePreference = typeof window === 'undefined' ? 'system' : readCache();
let snapshot = { preference: current, resolved: 'light' as 'light' | 'dark' };
const listeners = new Set<() => void>();
let saver: ((p: ThemePreference) => void) | null = null;

export function resolveTheme(p: ThemePreference): 'light' | 'dark' {
  return p === 'system' ? (darkQuery().matches ? 'dark' : 'light') : p;
}

/** Applies a preference to <html> and caches it. */
export function applyTheme(p: ThemePreference): void {
  current = p;
  const dark = resolveTheme(p) === 'dark';
  snapshot = { preference: p, resolved: dark ? 'dark' : 'light' };
  const root = document.documentElement;
  root.classList.toggle('dark', dark);
  root.style.colorScheme = dark ? 'dark' : 'light';
  try {
    localStorage.setItem(THEME_CACHE_KEY, p);
  } catch {
    /* private mode */
  }
  listeners.forEach((l) => l());
}

let installed = false;
function install() {
  if (installed || typeof window === 'undefined') return;
  installed = true;
  applyTheme(current);
  darkQuery().addEventListener('change', () => current === 'system' && applyTheme('system'));
  window.addEventListener('storage', (e) => {
    if (e.key === THEME_CACHE_KEY && isPreference(e.newValue) && e.newValue !== current) applyTheme(e.newValue);
  });
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

const serverSnapshot = { preference: 'system' as ThemePreference, resolved: 'light' as const };

/** The current choice and a setter that also saves it (for toggles). */
export function useThemePreference() {
  install();
  const s = useSyncExternalStore(subscribe, () => snapshot, () => serverSnapshot);
  const setPreference = useCallback((p: ThemePreference) => {
    applyTheme(p);
    saver?.(p);
  }, []);
  return { ...s, setPreference };
}

/**
 * Keeps the theme in step with the person's saved preference. Mount once per
 * app; endpoint is the app's <api>/me/preferences. Pass enabled=false until
 * the person is signed in (choices then stay in this browser).
 */
export function useThemeSync(endpoint: string, enabled = true): void {
  install();
  const qc = useQueryClient();
  const key = ['preferences', endpoint];

  const q = useQuery({
    queryKey: key,
    queryFn: () => apiRequest<Preferences>(endpoint),
    enabled,
    staleTime: 30_000,
    refetchOnWindowFocus: true,
    retry: false,
  });

  const save = useMutation({
    mutationFn: (theme: ThemePreference) => apiRequest<Preferences>(endpoint, { method: 'PUT', body: { theme } }),
    onSuccess: (p) => qc.setQueryData(key, p),
  });

  useEffect(() => {
    const p = q.data;
    if (!p || p.synced === false || save.isPending) return;
    if (!p.updated_at && current !== 'system') {
      // First visit since sharing: keep the choice this browser already had.
      save.mutate(current);
      return;
    }
    if (isPreference(p.theme) && p.theme !== current) applyTheme(p.theme);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q.data]);

  const { mutate } = save;
  useEffect(() => {
    if (!enabled) return;
    saver = mutate;
    return () => {
      if (saver === mutate) saver = null;
    };
  }, [enabled, mutate]);
}
