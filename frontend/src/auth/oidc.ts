import { UserManager, WebStorageStateStore } from 'oidc-client-ts';

/** True when the SPA was built for Keycloak / OIDC (Bearer tokens). */
export function isOidcAuthMode(): boolean {
  return import.meta.env.VITE_AUTH_MODE === 'oidc';
}

let manager: UserManager | null | undefined;

/**
 * Lazily construct the oidc-client-ts UserManager. Returns null when OIDC
 * is enabled but misconfigured (missing authority/client id), so callers
 * can surface a clear error instead of a runtime crash.
 */
export function getUserManager(): UserManager | null {
  if (manager !== undefined) {
    return manager;
  }
  const authority = (import.meta.env.VITE_OIDC_AUTHORITY as string | undefined)?.trim();
  const client_id = (import.meta.env.VITE_OIDC_CLIENT_ID as string | undefined)?.trim();
  if (!authority || !client_id) {
    manager = null;
    return null;
  }
  if (typeof window === 'undefined') {
    manager = null;
    return null;
  }
  const scope = (import.meta.env.VITE_OIDC_SCOPE as string | undefined)?.trim() || 'openid profile email';
  manager = new UserManager({
    authority,
    client_id,
    redirect_uri: `${window.location.origin}/auth/callback`,
    post_logout_redirect_uri: `${window.location.origin}/login`,
    response_type: 'code',
    scope,
    automaticSilentRenew: true,
    silent_redirect_uri: `${window.location.origin}/silent-callback.html`,
    monitorSession: true,
    // Shared by all tabs so a sign-out / user switch reaches every tab (see session.ts).
    userStore: new WebStorageStateStore({ store: window.localStorage }),
  });
  return manager;
}

export async function signInWithOidc(returnPath: string): Promise<void> {
  const mgr = getUserManager();
  if (!mgr) {
    throw new Error('OIDC is not configured (set VITE_OIDC_AUTHORITY and VITE_OIDC_CLIENT_ID).');
  }
  const safePath = returnPath.startsWith('/') ? returnPath : '/inbox';
  try {
    await mgr.signinRedirect({ state: safePath });
  } catch (err) {
    // fetch() rejects with a bare TypeError ("Failed to fetch") when the login
    // server is down or its certificate isn't trusted.
    if (err instanceof TypeError) {
      throw new Error(
        `Can't reach the login server (${new URL(mgr.settings.authority).host}). It may be down or its certificate may have expired.`,
      );
    }
    throw err;
  }
}

export async function signOutOidc(): Promise<void> {
  const mgr = getUserManager();
  if (!mgr) {
    return;
  }
  await mgr.signoutRedirect();
}
