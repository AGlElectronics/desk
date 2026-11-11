import { setAuthHeadersResolver } from '@/api/client';
import { installDevAuth } from '@/auth/dev';
import { ensureUser, isOidcAuthMode } from '@/auth/oidc';
import { installSessionSync } from '@/auth/session';

/** Wire the API client's auth header pipeline. Call once at startup. */
export function installAuth(): void {
  if (isOidcAuthMode()) {
    setAuthHeadersResolver(async (): Promise<Record<string, string>> => {
      const user = await ensureUser();
      // Access token only. The API refuses ID tokens and checks audience desk-api.
      const bearer = user?.access_token;
      if (!user || !bearer || user.expired) {
        return {};
      }
      return { Authorization: `Bearer ${bearer}` };
    });
    installSessionSync();
    return;
  }
  installDevAuth();
}
