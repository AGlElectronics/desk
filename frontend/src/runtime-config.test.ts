import { afterEach, describe, expect, it, vi } from 'vitest';

// A fresh module per test: the loaded settings are module state.
async function load(status: number, body: string, contentType = 'application/json') {
  vi.resetModules();
  const fetchMock = vi.fn(async () => new Response(body, { status, headers: { 'content-type': contentType } }));
  vi.stubGlobal('fetch', fetchMock);
  const mod = await import('./runtime-config');
  return { mod, fetchMock, result: mod.loadRuntimeConfig() };
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('loadRuntimeConfig', () => {
  it('fetches /config.json uncached and merges it over the build-time values', async () => {
    const { mod, fetchMock, result } = await load(
      200,
      JSON.stringify({
        authMode: 'oidc',
        oidcAuthority: ' https://auth.example.org/realms/conservices ',
        oidcClientId: 'example-spa',
        unknownKey: 'ignored',
      }),
    );
    const cfg = await result;
    expect(fetchMock).toHaveBeenCalledWith('/config.json', expect.objectContaining({ cache: 'no-store' }));
    expect(cfg).toMatchObject({
      authMode: 'oidc',
      oidcAuthority: 'https://auth.example.org/realms/conservices',
      oidcClientId: 'example-spa',
      oidcScope: 'openid profile email',
      apiBaseUrl: '',
    });
    expect(cfg).not.toHaveProperty('unknownKey');
    expect(mod.runtimeConfig()).toEqual(cfg);
  });

  it('keeps the build-time values when the file is missing', async () => {
    const { mod, result } = await load(404, 'not found', 'text/plain');
    const before = mod.runtimeConfig();
    await expect(result).resolves.toEqual(before);
  });

  it('treats the dev server answering with index.html as a missing file', async () => {
    const { mod, result } = await load(200, '<!doctype html>', 'text/html');
    await expect(result).resolves.toEqual(mod.runtimeConfig());
  });

  it('rejects a file that is not JSON', async () => {
    const { result } = await load(200, '{ authMode: oidc');
    await expect(result).rejects.toMatchObject({ title: 'Invalid settings in /config.json' });
  });

  it('rejects a value of the wrong type', async () => {
    const { result } = await load(200, JSON.stringify({ oidcAuthority: 42 }));
    await expect(result).rejects.toMatchObject({ detail: '"oidcAuthority" must be a string.' });
  });

  it('rejects an unknown auth mode', async () => {
    const { result } = await load(200, JSON.stringify({ authMode: 'none' }));
    await expect(result).rejects.toMatchObject({ detail: '"authMode" must be "oidc" or "dev".' });
  });

  it('reports "not configured" for OIDC without an authority or client id', async () => {
    const { result } = await load(200, JSON.stringify({ authMode: 'oidc', oidcAuthority: '', oidcClientId: '' }));
    await expect(result).rejects.toMatchObject({ title: 'Not configured yet' });
  });
});
