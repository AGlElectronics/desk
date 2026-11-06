import type { User } from 'oidc-client-ts';

import { setUnauthorizedHandler } from '@/api/client';
import { getUserManager } from '@/auth/oidc';

/*
 * Keeps every tab on the identity Keycloak currently has. The OIDC user lives
 * in localStorage (shared by all tabs of this app), so:
 *  - another tab signs out            -> this tab goes to the login page
 *  - another tab signs in as someone  -> this tab reloads as that person
 *  - Keycloak session ends elsewhere   -> session monitor signs this tab out
 *  - the API answers 401               -> back to the login page
 * Without this, a tab kept a stale admin token after a user switch.
 */

const USER_KEY_PREFIX = 'oidc.user:';

function subOf(raw: string | null): string | undefined {
  if (!raw) return undefined;
  try {
    return (JSON.parse(raw) as { profile?: { sub?: string } }).profile?.sub;
  } catch {
    return undefined;
  }
}

export function goToLogin(): void {
  const path = `${window.location.pathname}${window.location.search}`;
  if (path.startsWith('/login') || path.startsWith('/auth/')) return;
  window.location.assign(`/login?redirect=${encodeURIComponent(path)}`);
}

let installed = false;

export function installSessionSync(): void {
  const mgr = getUserManager();
  if (!mgr || installed || typeof window === 'undefined') return;
  installed = true;

  let currentSub: string | undefined;
  void mgr.getUser().then((u) => {
    currentSub = u?.profile.sub;
  });
  mgr.events.addUserLoaded((u: User) => {
    currentSub = u.profile.sub;
  });

  window.addEventListener('storage', (e) => {
    if (e.storageArea !== window.localStorage) return;
    if (e.key !== null && !e.key.startsWith(USER_KEY_PREFIX)) return;
    const next = e.key === null ? undefined : subOf(e.newValue);
    if (next === currentSub) return; // token refresh for the same person
    if (next === undefined) {
      goToLogin();
    } else {
      window.location.reload();
    }
  });

  const endSession = () => {
    void mgr.removeUser().finally(goToLogin);
  };
  mgr.events.addUserSignedOut(endSession);
  // Same person, new Keycloak session (signed out and back in elsewhere): adopt
  // it silently. A reload would loop, because the stored user keeps the old
  // session_state and the monitor reports "changed" again on every load.
  mgr.events.addUserSessionChanged(() => {
    void mgr.signinSilent().catch(endSession);
  });
  mgr.events.addSilentRenewError((err) => {
    if (/login_required|invalid_grant|session not active/i.test(String(err?.message ?? err))) endSession();
  });

  let redirecting = false;
  setUnauthorizedHandler(() => {
    if (redirecting) return;
    redirecting = true;
    endSession();
  });
}
