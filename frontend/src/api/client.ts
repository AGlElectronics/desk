// Typed fetch wrapper. The backend speaks the same error shape as Stash
// ({"error":{"code","message","details"}}), so this file is a near-copy of
// the Stash client with the dev-user header normalized to work for Desk
// too. The auth-headers resolver is injected at startup from
// src/auth/installAuth.ts.

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
      if (v === undefined || v === null) continue;
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

  if (res.status === 401) {

    unauthorizedHandler?.();

  }

  if (res.status === 204) {
    return undefined as T;
  }

  const text = await res.text();
  const data = text ? JSON.parse(text) : null;

  if (!res.ok) {
    throw new ApiError(res.status, data as ApiErrorBody);
  }
  return data as T;
}

/** Downloads an authenticated file and hands it to the browser as filename. */
export async function apiDownload(path: string, filename: string): Promise<void> {
  const auth = await authHeadersResolver();
  const res = await fetch(`${apiBaseUrl()}${path}`, { headers: auth, credentials: 'include' });
  if (!res.ok) {
    throw new Error(`Download failed (${res.status})`);
  }
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
