// Dev auth shim. When the backend runs with AUTH_MODE=dev it accepts an
// X-Dev-User header; the SPA sends whatever is in localStorage (defaulting
// to "dev-admin"). The real OIDC flow lives in oidc.ts.

import { setAuthHeadersResolver } from '@/api/client';

const STORAGE_KEY = 'desk.devUser';

export function getDevUser(): string {
  if (typeof window === 'undefined') return 'dev-admin';
  return window.localStorage.getItem(STORAGE_KEY) ?? 'dev-admin';
}

export function setDevUser(name: string) {
  window.localStorage.setItem(STORAGE_KEY, name);
}

export function installDevAuth() {
  setAuthHeadersResolver(async () => ({
    'X-Dev-User': getDevUser(),
  }));
}
