import { useEffect, useState } from 'react';
import { useNavigate } from '@tanstack/react-router';
import { LoadingRow } from '@/components/ui/States';
import { oidcCallbackUrl } from '@/auth/captureCallbackUrl';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';

/**
 * Completes the OIDC Authorization Code flow. We capture the URL at module
 * evaluation time (captureCallbackUrl) because TanStack Router strips
 * unvalidated query params before this component mounts.
 *
 * In dev mode this page should never be reached; we send the user straight
 * to the dashboard so the SPA doesn't dead-end if someone bookmarks
 * /auth/callback after switching AUTH_MODE back to dev.
 */
export function OidcCallbackPage() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!isOidcAuthMode()) {
      navigate({ to: '/inbox' }).catch(() => undefined);
      return;
    }
    const mgr = getUserManager();
    if (!mgr) {
      setError('OIDC is not configured (set VITE_OIDC_AUTHORITY and VITE_OIDC_CLIENT_ID).');
      return;
    }
    let cancelled = false;
    mgr
      .signinRedirectCallback(oidcCallbackUrl())
      .then((user) => {
        if (cancelled) return;
        const target =
          typeof user?.state === 'string' && user.state.startsWith('/')
            ? user.state
            : '/inbox';
        navigate({ to: target, replace: true }).catch(() => undefined);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err instanceof Error ? err.message : 'Sign-in failed.');
      });
    return () => {
      cancelled = true;
    };
  }, [navigate]);

  if (error) {
    return (
      <div className="min-h-screen flex items-center justify-center text-center px-4 bg-background text-foreground">
        <div className="max-w-md space-y-3">
          <h1 className="font-heading text-2xl font-extrabold">Sign-in failed</h1>
          <p className="text-sm text-destructive">{error}</p>
          <p className="text-sm text-muted-foreground">
            Return to <a className="underline" href="/login">the login page</a> and try again.
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background text-foreground">
      <LoadingRow message="Completing sign-in…" />
    </div>
  );
}
