import { InMemoryWebStorage, UserManager, WebStorageStateStore, type User } from 'oidc-client-ts';
import { runtimeConfig } from '@/runtime-config';

/** True when the SPA was built for Keycloak / OIDC (Bearer tokens). */
export function isOidcAuthMode(): boolean {
  return runtimeConfig().authMode === 'oidc';
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
  const authority = runtimeConfig().oidcAuthority;
  const client_id = runtimeConfig().oidcClientId;
  if (!authority || !client_id) {
    manager = null;
    return null;
  }
  if (typeof window === 'undefined') {
    manager = null;
    return null;
  }
  const scope = runtimeConfig().oidcScope;
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
    // Token contract: tokens live in memory only, never in localStorage or
    // sessionStorage. After a reload ensureUser signs in again silently.
    userStore: new WebStorageStateStore({ store: new InMemoryWebStorage() }),
  });
  return manager;
}

let silent: Promise<User | null> | null = null;

/**
 * The signed-in user, signing in silently (hidden iframe, prompt=none)
 * when memory holds none, for example after a reload. Null when there is
 * no Keycloak session. Concurrent callers share one attempt.
 */
export async function ensureUser(): Promise<User | null> {
  const mgr = getUserManager();
  if (!mgr) return null;
  const user = await mgr.getUser();
  if (user && !user.expired) return user;
  silent ??= mgr
    .signinSilent()
    .catch(() => null)
    .finally(() => {
      silent = null;
    });
  return silent;
}

export async function signInWithOidc(returnPath: string): Promise<void> {
  const mgr = getUserManager();
  if (!mgr) {
    throw new Error('OIDC is not configured (set oidcAuthority and oidcClientId in /config.json).');
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
