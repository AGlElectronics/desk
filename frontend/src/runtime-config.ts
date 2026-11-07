// Installation-specific settings, read once at start-up from /config.json so
// one image works for any domain. nginx serves the file from
// /etc/conservices/config.json (a ConfigMap in Kubernetes, a bind mount in
// Docker Compose). Keys the file leaves out keep their build-time value from
// import.meta.env, so `npm run dev` with a .env file keeps working.
// Docs: https://aglelectronics.github.io/con-services/modules/desk/configuration/#web-app-settings-configjson

export type AuthMode = 'dev' | 'oidc';

export type RuntimeConfig = {
  authMode: AuthMode;
  oidcAuthority: string;
  oidcClientId: string;
  oidcScope: string;
  apiBaseUrl: string;
  productName: string;
};

const KEYS: Record<keyof RuntimeConfig, 'string' | 'boolean'> = {
  authMode: 'string',
  oidcAuthority: 'string',
  oidcClientId: 'string',
  oidcScope: 'string',
  apiBaseUrl: 'string',
  productName: 'string',
};

const DEFAULT_SCOPE = 'openid profile email';

const buildTime: RuntimeConfig = {
  // A production build never falls back to dev sign-in: without "authMode"
  // in /config.json it signs in through OIDC, whatever it was built with.
  authMode: import.meta.env.DEV && import.meta.env.VITE_AUTH_MODE !== 'oidc' ? 'dev' : 'oidc',
  oidcAuthority: (import.meta.env.VITE_OIDC_AUTHORITY ?? '').trim(),
  oidcClientId: (import.meta.env.VITE_OIDC_CLIENT_ID ?? '').trim(),
  oidcScope: (import.meta.env.VITE_OIDC_SCOPE ?? '').trim() || DEFAULT_SCOPE,
  apiBaseUrl: (import.meta.env.VITE_API_BASE_URL ?? '').trim(),
  productName: (import.meta.env.VITE_PRODUCT_NAME ?? '').trim(),
};

let current: RuntimeConfig = buildTime;

/** The settings in effect. Until loadRuntimeConfig() resolves (and in unit tests): the build-time values. */
export function runtimeConfig(): RuntimeConfig {
  return current;
}

/** /config.json can't be used; the page shows title and detail instead of the app. */
export class RuntimeConfigError extends Error {
  readonly title: string;
  readonly detail: string;

  constructor(title: string, detail: string) {
    super(`${title}: ${detail}`);
    this.name = 'RuntimeConfigError';
    this.title = title;
    this.detail = detail;
  }
}

const invalid = (detail: string) => new RuntimeConfigError('Invalid settings in /config.json', detail);

/**
 * Fetches /config.json and merges it over the build-time values. A missing
 * file (404) keeps the build-time values. Throws RuntimeConfigError when the
 * file is not valid JSON, has a value of the wrong type, or leaves sign-in
 * unconfigured.
 */
export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  let res: Response;
  try {
    res = await fetch('/config.json', { cache: 'no-store', headers: { Accept: 'application/json' } });
  } catch {
    throw new RuntimeConfigError(
      'Could not load the settings',
      'The request for /config.json failed. Check your connection and reload the page.',
    );
  }
  // The development server answers unknown paths with index.html.
  const html = (res.headers.get('content-type') ?? '').includes('text/html');
  if (res.status === 404 || (res.ok && html)) {
    current = checked(buildTime);
    return current;
  }
  if (!res.ok) {
    throw new RuntimeConfigError('Could not load the settings', `/config.json answered HTTP ${res.status}.`);
  }
  let raw: unknown;
  try {
    raw = JSON.parse(await res.text());
  } catch {
    throw invalid('The file is not valid JSON. Ask your administrator to fix it.');
  }
  current = checked(merged(buildTime, raw));
  return current;
}

function merged(base: RuntimeConfig, raw: unknown): RuntimeConfig {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    throw invalid('The file must contain a JSON object.');
  }
  const next: Record<string, unknown> = { ...base };
  for (const [key, type] of Object.entries(KEYS)) {
    const value = (raw as Record<string, unknown>)[key];
    if (value === undefined || value === null) continue;
    if (typeof value !== type) throw invalid(`"${key}" must be a ${type}.`);
    next[key] = typeof value === 'string' ? value.trim() : value;
  }
  return next as RuntimeConfig;
}

function checked(cfg: RuntimeConfig): RuntimeConfig {
  if (cfg.authMode !== 'oidc' && cfg.authMode !== 'dev') {
    throw invalid('"authMode" must be "oidc" or "dev".');
  }
  if (cfg.authMode === 'oidc' && (!cfg.oidcAuthority || !cfg.oidcClientId)) {
    throw new RuntimeConfigError(
      'Not configured yet',
      'This web app does not know where to sign in. Set "oidcAuthority" and "oidcClientId" in /config.json; the configuration page of the documentation shows how.',
    );
  }
  return { ...cfg, oidcScope: cfg.oidcScope || DEFAULT_SCOPE };
}

/** Replaces the page with a plain error message (no styles needed, nothing inline). */
export function renderStartupError(root: HTMLElement, err: unknown): void {
  const title = err instanceof RuntimeConfigError ? err.title : 'The app could not start';
  const detail = err instanceof RuntimeConfigError ? err.detail : err instanceof Error ? err.message : String(err);
  const box = document.createElement('div');
  box.setAttribute('role', 'alert');
  Object.assign(box.style, {
    maxWidth: '36rem',
    margin: '15vh auto',
    padding: '0 1.5rem',
    fontFamily: 'system-ui, sans-serif',
    lineHeight: '1.5',
  });
  const heading = document.createElement('h1');
  heading.textContent = title;
  heading.style.fontSize = '1.25rem';
  const text = document.createElement('p');
  text.textContent = detail;
  box.append(heading, text);
  root.replaceChildren(box);
}
