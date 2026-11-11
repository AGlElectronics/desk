import { describe, expect, it } from 'vitest';
import { EMAIL_SANDBOX, emailDocument, inlineAttachmentIds, rewriteInlineImages } from './emailHtml';

const A = '11111111-2222-3333-4444-555555555555';
const B = '66666666-7777-8888-9999-000000000000';

describe('inline images', () => {
  const html = `<p>Hi</p><img src="/api/v1/attachments/${A}"><img src="/api/v1/attachments/${B}"><img src="/api/v1/attachments/${A}">`;

  it('lists each referenced attachment once', () => {
    expect(inlineAttachmentIds(html)).toEqual([A, B]);
  });

  it('ignores other image sources', () => {
    expect(inlineAttachmentIds('<img src="https://tracker.example/p.gif"><img src="/api/v1/attachments/nope">')).toEqual([]);
  });

  it('swaps loaded images for data: URLs and leaves the rest', () => {
    const out = rewriteInlineImages(html, { [A]: 'data:image/png;base64,AAA' });
    expect(out).toContain('src="data:image/png;base64,AAA"');
    expect(out).toContain(`src="/api/v1/attachments/${B}"`);
    expect(out).not.toContain(`/api/v1/attachments/${A}`);
  });
});

describe('emailDocument', () => {
  it('blocks scripts, forms and remote images until allowed', () => {
    const doc = emailDocument('<p>x</p>', false);
    expect(doc).toContain("default-src 'none'");
    expect(doc).toContain('img-src data:;');
    expect(doc).toContain("form-action 'none'");
    expect(doc).toContain('<meta name="referrer" content="no-referrer">');
    expect(doc).toContain('<body><p>x</p></body>');
  });

  it('allows https images after "Show images"', () => {
    expect(emailDocument('', true)).toContain('img-src https: data:;');
  });
});

describe('EMAIL_SANDBOX', () => {
  it('never lets scripts run or popups escape the sandbox', () => {
    const flags = EMAIL_SANDBOX.split(' ');
    expect(flags).not.toContain('allow-scripts');
    expect(flags).not.toContain('allow-popups-to-escape-sandbox');
    expect(flags).toContain('allow-popups');
  });
});
