import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError, apiRequest, setUnauthorizedHandler, toErrorBody } from './client';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('toErrorBody', () => {
  it('keeps the API error shape', () => {
    const body = JSON.stringify({ error: { code: 'validation', message: 'Check the form.', details: { email: 'required' } } });
    expect(toErrorBody(422, body)).toEqual(JSON.parse(body));
  });

  it('turns an HTML error page into a generic error', () => {
    expect(toErrorBody(502, '<html>Bad gateway</html>')).toEqual({
      error: { code: 'http_502', message: 'The server answered 502.' },
    });
  });

  it('says so when the body is empty', () => {
    expect(toErrorBody(500, '').error.message).toBe('The server answered 500 without details.');
  });
});

describe('apiRequest', () => {
  it('throws an ApiError with field messages', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        new Response(JSON.stringify({ error: { code: 'validation', message: 'Invalid', details: { slug: 'taken' } } }), {
          status: 422,
        })
      )
    );
    const err = await apiRequest('/api/v1/mailboxes').catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect((err as ApiError).status).toBe(422);
    expect((err as ApiError).fields()).toEqual({ slug: 'taken' });
  });

  it('calls the unauthorized handler on 401', async () => {
    const onUnauthorized = vi.fn();
    setUnauthorizedHandler(onUnauthorized);
    vi.stubGlobal('fetch', vi.fn(async () => new Response('', { status: 401 })));
    await expect(apiRequest('/api/v1/me')).rejects.toBeInstanceOf(ApiError);
    expect(onUnauthorized).toHaveBeenCalledOnce();
  });

  it('drops empty query values and returns undefined for 204', async () => {
    const fetchMock = vi.fn(async (_url: string) => new Response(null, { status: 204 }));
    vi.stubGlobal('fetch', fetchMock);
    await expect(apiRequest('/api/v1/conversations', { query: { folder: 'mine', q: '', cursor: undefined } })).resolves.toBeUndefined();
    expect(fetchMock.mock.calls[0][0]).toMatch(/\/api\/v1\/conversations\?folder=mine$/);
  });
});
