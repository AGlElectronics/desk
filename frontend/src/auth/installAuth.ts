import { setAuthHeadersResolver } from '@/api/client';
import { installDevAuth } from '@/auth/dev';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';
import { installSessionSync } from '@/auth/session';

/** Wire the API client's auth header pipeline. Call once at startup. */
export function installAuth(): void {
  if (isOidcAuthMode()) {
    setAuthHeadersResolver(async (): Promise<Record<string, string>> => {
      const mgr = getUserManager();
      if (!mgr) {
        return {};
      }
      const user = await mgr.getUser();
      if (!user) {
        return {};
      }
      // Desk's backend uses go-oidc's IDTokenVerifier (expects an
      // ID-token-shaped JWT, typically aud=client_id). Prefer id_token for
      // this first-party API; fall back to access_token for unusual IdPs.
      const bearer = user.id_token ?? user.access_token;
      if (!bearer || user.expired) {
        return {};
      }
      return { Authorization: `Bearer ${bearer}` };
    });
    installSessionSync();
    return;
  }
  installDevAuth();
}
