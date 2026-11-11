import { render, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { EmailBody } from './EmailBody';

const ID = '11111111-2222-3333-4444-555555555555';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('EmailBody', () => {
  it('renders the mail in a sandboxed frame without scripts', () => {
    const { getByTitle } = render(<EmailBody html="<p>Hello</p>" showImages={false} />);
    const frame = getByTitle('Email');
    expect(frame).toHaveAttribute('sandbox', 'allow-same-origin allow-popups');
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer');
    const doc = frame.getAttribute('srcdoc') ?? '';
    expect(doc).toContain('<p>Hello</p>');
    expect(doc).toContain('img-src data:;');
  });

  it('loads inline images with the user token and embeds them as data: URLs', async () => {
    // jsdom's FileReader only reads jsdom Blobs, not the one Response.blob() makes.
    const png = new Blob(['png'], { type: 'image/png' });
    const fetchMock = vi.fn(async () => ({ ok: true, status: 200, blob: async () => png }));
    vi.stubGlobal('fetch', fetchMock);
    const { getByTitle } = render(<EmailBody html={`<img src="/api/v1/attachments/${ID}">`} showImages={false} />);
    await waitFor(() => expect(getByTitle('Email').getAttribute('srcdoc')).toContain('src="data:image/png;base64,'));
    expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining(`/api/v1/attachments/${ID}`), expect.anything());
  });

  it('keeps the original reference when an inline image fails to load', async () => {
    const fetchMock = vi.fn(async () => new Response('gone', { status: 404 }));
    vi.stubGlobal('fetch', fetchMock);
    const { getByTitle } = render(<EmailBody html={`<img src="/api/v1/attachments/${ID}">`} showImages />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const doc = getByTitle('Email').getAttribute('srcdoc') ?? '';
    expect(doc).toContain(`/api/v1/attachments/${ID}`);
    expect(doc).toContain('img-src https: data:;');
  });
});
