// Captured at module-evaluation time (before the router initializes) so
// OIDC query params (code, state, session_state) survive TanStack Router's
// URL normalization — the router strips unvalidated search params.

const _captured =
  typeof window !== 'undefined' && window.location.pathname === '/auth/callback'
    ? window.location.href
    : '';

export function oidcCallbackUrl(): string {
  return _captured || (typeof window !== 'undefined' ? window.location.href : '');
}
