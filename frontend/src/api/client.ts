// Typed fetch wrapper for Desk's API. Errors come back as
// {"error":{"code","message","details"}}; anything else (a proxy's HTML
// error page, an empty body) becomes an ApiError with the HTTP status. The
// auth-headers resolver is installed at startup by src/auth/installAuth.ts.

import { runtimeConfig } from '@/runtime-config';

const apiBaseUrl = () =>
  (typeof process !== 'undefined' ? process.env.DESK_API_BASE_URL : undefined) ??
  runtimeConfig().apiBaseUrl;

export type ApiErrorBody = {
  error: {
    code: string;
    message: string;
    details?: unknown;
  };
};

export class ApiError extends Error {
  status: number;
  code: string;
  details?: unknown;

  constructor(status: number, body: ApiErrorBody) {
    super(body.error.message);
    this.status = status;
    this.code = body.error.code;
    this.details = body.error.details;
  }

  /** Field messages of a validation error ({"field": "message"}). */
  fields(): Record<string, string> {
    return this.details && typeof this.details === 'object' ? (this.details as Record<string, string>) : {};
  }
}

/** Turns any response body into the error shape. */
export function toErrorBody(status: number, text: string): ApiErrorBody {
  try {
    const parsed = JSON.parse(text) as Partial<ApiErrorBody>;
    if (parsed && parsed.error && typeof parsed.error.message === 'string') {
      return parsed as ApiErrorBody;
    }
  } catch {
    // not JSON: fall through
  }
  return { error: { code: `http_${status}`, message: `The server answered ${status}${text ? '' : ' without details'}.` } };
}

type AuthHeaders = Record<string, string>;
let authHeadersResolver: () => Promise<AuthHeaders> = async () => ({});

/** installAuth.ts calls this to swap in dev-header or OIDC-bearer logic. */
export function setAuthHeadersResolver(fn: () => Promise<AuthHeaders>) {
  authHeadersResolver = fn;
}

let unauthorizedHandler: (() => void) | null = null;

/** Called when the API answers 401 (session gone); set by the OIDC session sync. */
export function setUnauthorizedHandler(fn: () => void) {
  unauthorizedHandler = fn;
}

type RequestOpts = {
  method?: 'GET' | 'POST' | 'PATCH' | 'PUT' | 'DELETE';
  body?: unknown;
  signal?: AbortSignal;
  query?: Record<string, string | number | boolean | undefined | null>;
  headers?: Record<string, string>;
};

export async function apiRequest<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const url = new URL(
    path.startsWith('http') ? path : `${apiBaseUrl()}${path}`,
    typeof window !== 'undefined' ? window.location.origin : 'http://localhost'
  );
  if (opts.query) {
    for (const [k, v] of Object.entries(opts.query)) {
      if (v === undefined || v === null || v === '') continue;
      url.searchParams.set(k, String(v));
    }
  }

  const auth = await authHeadersResolver();
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(opts.body !== undefined ? { 'Content-Type': 'application/json' } : {}),
    ...auth,
    ...(opts.headers ?? {}),
  };

  const res = await fetch(url.toString(), {
    method: opts.method ?? 'GET',
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    signal: opts.signal,
    headers,
    credentials: 'include',
  });

  if (res.status === 401) unauthorizedHandler?.();
  if (res.status === 204) return undefined as T;

  const text = await res.text();
  if (!res.ok) throw new ApiError(res.status, toErrorBody(res.status, text));
  return (text ? JSON.parse(text) : null) as T;
}

/** Fetches an authenticated file as a Blob. */
export async function apiBlob(path: string, signal?: AbortSignal): Promise<Blob> {
  const auth = await authHeadersResolver();
  const res = await fetch(`${apiBaseUrl()}${path}`, { headers: auth, credentials: 'include', signal });
  if (!res.ok) throw new ApiError(res.status, toErrorBody(res.status, await res.text()));
  return res.blob();
}

/** Downloads an authenticated file and hands it to the browser as filename. */
export async function apiDownload(path: string, filename: string): Promise<void> {
  const url = URL.createObjectURL(await apiBlob(path));
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
