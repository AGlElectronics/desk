// Helpers for showing a customer's (server-sanitised) email HTML.

const ATTACHMENT_SRC = /src="\/api\/v1\/attachments\/([0-9a-f-]{36})"/g;

/** Attachment ids referenced by inline images (cid: parts the server rewrote). */
export function inlineAttachmentIds(html: string): string[] {
  return [...new Set([...html.matchAll(ATTACHMENT_SRC)].map((m) => m[1]))];
}

/**
 * Replaces inline image URLs with data: URLs. The email frame cannot send
 * the user's token, so the app loads those images itself first.
 */
export function rewriteInlineImages(html: string, dataUrls: Record<string, string>): string {
  return html.replace(ATTACHMENT_SRC, (whole, id: string) => (dataUrls[id] ? `src="${dataUrls[id]}"` : whole));
}

/**
 * The document inside the email frame. The CSP blocks scripts, forms,
 * frames and (until "Show images") remote images; links open in a new tab
 * without a referrer.
 */
export function emailDocument(html: string, showRemoteImages: boolean): string {
  const img = showRemoteImages ? 'https: data:' : 'data:';
  return `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${img}; style-src 'unsafe-inline'; form-action 'none'">
<meta name="referrer" content="no-referrer">
<base target="_blank"><style>body{margin:0;font:14px/1.5 system-ui,sans-serif;color:#222;word-wrap:break-word}
img{max-width:100%;height:auto}blockquote{margin:0 0 0 .5rem;padding-left:.75rem;border-left:3px solid #ddd;color:#666}</style>
</head><body>${html}</body></html>`;
}

/**
 * Sandbox flags for the email frame: same origin only so the app can
 * measure the height (scripts stay off), popups so links open, but a popup
 * stays sandboxed (no allow-popups-to-escape-sandbox).
 */
export const EMAIL_SANDBOX = 'allow-same-origin allow-popups';

export function blobToDataUrl(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(b);
  });
}
