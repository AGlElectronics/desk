import { useEffect, useState } from 'react';
import { useNavigate, useRouterState } from '@tanstack/react-router';
import { GraduationCap, LogIn } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { LoginBackdrop, loginLook } from '@/components/LoginBackdrop';
import { useBrand } from '@/brand/BrandProvider';
import { getDevUser, setDevUser } from '@/auth/dev';
import { isOidcAuthMode, signInWithOidc } from '@/auth/oidc';

/**
 * Login shim. In dev mode this is just a "pick a dev user name" form that
 * writes to localStorage — the API client stamps X-Dev-User from there on
 * every request. In OIDC mode the user is bounced to the IdP; after the
 * redirect lands on /auth/callback the AuthCallbackPage completes the flow.
 */
export function LoginPage() {
  const { brand } = useBrand();
  const navigate = useNavigate();
  const search = useRouterState({ select: (s) => s.location.search as { redirect?: string } });
  const redirect = typeof search.redirect === 'string' && search.redirect.startsWith('/')
    ? search.redirect
    : '/inbox';
  const oidc = isOidcAuthMode();
  const [devName, setDevNameState] = useState(getDevUser());
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
  }, [devName]);

  const continueDev = (event: React.FormEvent) => {
    event.preventDefault();
    const trimmed = devName.trim();
    if (!trimmed) {
      setError('Pick a dev user name (anything non-empty).');
      return;
    }
    setDevUser(trimmed);
    navigate({ to: redirect }).catch(() => undefined);
  };

  const continueOidc = async () => {
    setSubmitting(true);
    try {
      await signInWithOidc(redirect);
    } catch (err) {
      setSubmitting(false);
      setError(err instanceof Error ? err.message : 'Could not start sign-in.');
    }
  };

  const look = loginLook(brand.loginBackgroundUrl, brand.loginLayout, brand.loginCaption);

  return (
    <LoginBackdrop look={look} plain="min-h-screen flex items-center justify-center bg-background text-foreground px-4 py-10">
      <Card className="login-card w-full max-w-md">
        <CardHeader>
          <div className="flex items-center gap-2.5">
            <span className="rounded-pill bg-primary-soft p-1.5">
              <GraduationCap className="w-5 h-5 text-primary" />
            </span>
            <CardTitle className="text-xl">{brand.productName}</CardTitle>
          </div>
          <CardDescription>{brand.loginSubtitle ?? 'Sign in to continue.'}</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {oidc ? (
            <>
              <p className="text-sm text-muted-foreground">
                You will be redirected to your organisation login.
              </p>
              <Button
                type="button"
                className="w-full"
                onClick={() => void continueOidc()}
                disabled={submitting}
              >
                <LogIn className="w-4 h-4" />
                {brand.loginButtonLabel ?? 'Continue with your organisation login'}
              </Button>
            </>
          ) : (
            <form className="space-y-3" onSubmit={continueDev}>
              <div>
                <Label htmlFor="dev-user">Dev user name</Label>
                <Input
                  id="dev-user"
                  value={devName}
                  onChange={(e) => setDevNameState(e.target.value)}
                  placeholder="dev-admin"
                  autoFocus
                />
                <p className="text-xs text-muted-foreground mt-1">
                  AUTH_MODE=dev: the backend accepts any <code>X-Dev-User</code> value.
                </p>
              </div>
              <Button type="submit" className="w-full">
                <LogIn className="w-4 h-4" />
                Continue
              </Button>
            </form>
          )}
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
        </CardContent>
      </Card>
    </LoginBackdrop>
  );
}
