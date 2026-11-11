import { useEffect, useMemo, useRef, useState } from 'react';
import { apiBlob } from '@/api/client';
import { blobToDataUrl, EMAIL_SANDBOX, emailDocument, inlineAttachmentIds, rewriteInlineImages } from '@/lib/emailHtml';

/**
 * Renders a customer's (already sanitised) HTML in a sandboxed frame: no
 * scripts, inline images loaded with the user's token, remote images only
 * after "Show images".
 */
export function EmailBody({ html, showImages }: { html: string; showImages: boolean }) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(80);
  const [inline, setInline] = useState<Record<string, string>>({});
  const ids = useMemo(() => inlineAttachmentIds(html), [html]);

  useEffect(() => {
    if (ids.length === 0) return;
    const ctrl = new AbortController();
    void Promise.all(
      ids.map(async (id) => {
        try {
          return [id, await blobToDataUrl(await apiBlob(`/api/v1/attachments/${id}`, ctrl.signal))] as const;
        } catch {
          return [id, ''] as const;
        }
      })
    ).then((pairs) => {
      if (!ctrl.signal.aborted) setInline(Object.fromEntries(pairs.filter(([, url]) => url)));
    });
    return () => ctrl.abort();
  }, [ids]);

  const doc = useMemo(() => emailDocument(rewriteInlineImages(html, inline), showImages), [html, inline, showImages]);
  return (
    <iframe
      ref={ref}
      title="Email"
      sandbox={EMAIL_SANDBOX}
      referrerPolicy="no-referrer"
      srcDoc={doc}
      className="w-full rounded-md bg-white"
      style={{ height }}
      onLoad={() => {
        const body = ref.current?.contentDocument?.body;
        if (body) setHeight(Math.min(body.scrollHeight + 16, 4000));
      }}
    />
  );
}
