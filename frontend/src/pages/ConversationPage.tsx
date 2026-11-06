import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { AlertTriangle, ArrowLeft, Eye, ImageOff, Image, Paperclip, RotateCcw, Send, StickyNote, Undo2 } from 'lucide-react';
import {
  addThread,
  getConversation,
  listPeople,
  retryReply,
  sendHeartbeat,
  undoReply,
  updateConversation,
  updateCustomer,
  type ConversationDetail,
  type Status,
  type Thread,
} from '@/api/desk';
import { apiDownload } from '@/api/client';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { PageContainer } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { StatusBadge, STATUS_LABELS } from '@/components/StatusBadge';
import { useMe } from '@/hooks/useMe';
import { dateTime, fileSize, timeAgo } from '@/lib/format';
import { cn } from '@/lib/utils';

const selectClass = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

/**
 * Renders a customer's (already sanitised) HTML in a sandboxed frame:
 * no scripts, and remote images only after "Show images".
 */
function EmailBody({ html, showImages }: { html: string; showImages: boolean }) {
  const ref = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(80);
  const doc = useMemo(() => {
    const img = showImages ? 'https: data:' : 'data:';
    return `<!doctype html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${img}; style-src 'unsafe-inline'">
<base target="_blank"><style>body{margin:0;font:14px/1.5 system-ui,sans-serif;color:#222;word-wrap:break-word}
img{max-width:100%;height:auto}blockquote{margin:0 0 0 .5rem;padding-left:.75rem;border-left:3px solid #ddd;color:#666}</style>
</head><body>${html}</body></html>`;
  }, [html, showImages]);
  return (
    <iframe
      ref={ref}
      title="Email"
      sandbox="allow-same-origin allow-popups allow-popups-to-escape-sandbox"
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

function ThreadCard({ t, showImages }: { t: Thread; showImages: boolean }) {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['conversation'] });
  const undo = useMutation({ mutationFn: () => undoReply(t.id), onSettled: refresh });
  const retry = useMutation({ mutationFn: () => retryReply(t.id), onSettled: refresh });

  if (t.kind === 'system') {
    return (
      <p className="text-xs text-muted-foreground text-center py-1">
        {t.author_name ? `${t.author_name}: ` : ''}
        {t.body_text} · {timeAgo(t.created_at)}
      </p>
    );
  }
  const who = t.kind === 'customer' ? t.from_email : t.author_name || 'Agent';
  return (
    <Card
      className={cn(
        'overflow-hidden',
        t.kind === 'note' && 'border-warning/40 bg-warning/5',
        t.kind === 'reply' && 'border-primary/30'
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-2 border-b border-border text-sm">
        <span className="flex items-center gap-2">
          {t.kind === 'note' ? <StickyNote className="w-4 h-4 text-warning" /> : null}
          <span className="font-semibold">{who}</span>
          <span className="text-muted-foreground">
            {t.kind === 'customer' ? 'wrote' : t.kind === 'note' ? 'added a note' : `replied to ${t.to.join(', ')}`}
          </span>
        </span>
        <span className="flex items-center gap-2 text-xs text-muted-foreground">
          {t.send_status === 'queued' ? (
            <>
              <span>Sending…</span>
              {t.next_attempt_at && new Date(t.next_attempt_at) > new Date() && !t.send_error ? (
                <Button size="sm" variant="outline" onClick={() => undo.mutate()} disabled={undo.isPending}>
                  <Undo2 className="w-3.5 h-3.5" />
                  Undo
                </Button>
              ) : null}
            </>
          ) : null}
          {t.send_status === 'failed' ? (
            <>
              <span className="text-destructive" title={t.send_error}>
                Not sent
              </span>
              <Button size="sm" variant="outline" onClick={() => retry.mutate()} disabled={retry.isPending}>
                <RotateCcw className="w-3.5 h-3.5" />
                Retry
              </Button>
            </>
          ) : null}
          <span title={dateTime(t.created_at)}>{timeAgo(t.created_at)}</span>
        </span>
      </div>
      <div className="p-4">
        {t.kind === 'customer' ? (
          <EmailBody html={t.body_html} showImages={showImages} />
        ) : (
          <p className="text-sm whitespace-pre-wrap">{t.body_text}</p>
        )}
        {t.send_status === 'queued' && t.send_error ? (
          <p className="mt-2 text-xs text-destructive">Last attempt failed: {t.send_error}. Retrying automatically.</p>
        ) : null}
        {t.attachments.length > 0 ? (
          <div className="mt-3 flex flex-wrap gap-2">
            {t.attachments.map((a) => (
              <button
                key={a.id}
                type="button"
                onClick={() => void apiDownload(`/api/v1/attachments/${a.id}`, a.filename)}
                className="inline-flex items-center gap-1.5 rounded-pill border border-border px-3 py-1 text-xs hover:bg-secondary"
              >
                <Paperclip className="w-3.5 h-3.5" />
                {a.filename} · {fileSize(a.size_bytes)}
              </button>
            ))}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

function Composer({ c }: { c: ConversationDetail }) {
  const qc = useQueryClient();
  const [kind, setKind] = useState<'reply' | 'note'>('reply');
  const [body, setBody] = useState('');
  const [after, setAfter] = useState<Status>('pending');
  const typing = body.trim().length > 0;

  // Heartbeat: others see "viewing" or "replying" while this page is open.
  useEffect(() => {
    const beat = () => void sendHeartbeat(c.id, typing ? 'replying' : 'viewing').catch(() => undefined);
    beat();
    const t = setInterval(beat, 10_000);
    return () => clearInterval(t);
  }, [c.id, typing]);

  const send = useMutation({
    mutationFn: () => addThread(c.id, { kind, body, status: kind === 'reply' ? after : undefined }),
    onSuccess: () => {
      setBody('');
      void qc.invalidateQueries({ queryKey: ['conversation'] });
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
    },
  });

  return (
    <Card className={cn('p-4', kind === 'note' && 'border-warning/40 bg-warning/5')}>
      <div className="flex gap-1 mb-3">
        {(['reply', 'note'] as const).map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => setKind(k)}
            className={cn(
              'px-3 py-1.5 rounded-pill text-sm font-medium',
              kind === k ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-secondary'
            )}
          >
            {k === 'reply' ? 'Reply' : 'Note'}
          </button>
        ))}
        <span className="ml-auto text-xs text-muted-foreground self-center">
          {kind === 'reply' ? `To ${c.customer.email}` : 'Only visible to the team'}
        </span>
      </div>
      <textarea
        rows={6}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        placeholder={kind === 'reply' ? 'Write your reply…' : 'Write an internal note…'}
        className="w-full rounded-md border border-input bg-background p-3 text-sm"
      />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button onClick={() => send.mutate()} disabled={!typing || send.isPending}>
          {kind === 'reply' ? <Send className="w-4 h-4" /> : <StickyNote className="w-4 h-4" />}
          {kind === 'reply' ? 'Send reply' : 'Add note'}
        </Button>
        {kind === 'reply' ? (
          <label className="text-sm text-muted-foreground flex items-center gap-2">
            then set status
            <select className={selectClass} value={after} onChange={(e) => setAfter(e.target.value as Status)}>
              <option value="active">Active</option>
              <option value="pending">Pending</option>
              <option value="closed">Closed</option>
            </select>
          </label>
        ) : null}
        {send.isError ? <span className="text-sm text-destructive">{(send.error as Error).message}</span> : null}
      </div>
    </Card>
  );
}

function CustomerCard({ c }: { c: ConversationDetail }) {
  const qc = useQueryClient();
  const [notes, setNotes] = useState(c.customer.notes);
  useEffect(() => setNotes(c.customer.notes), [c.customer.id, c.customer.notes]);
  const save = useMutation({
    mutationFn: () => updateCustomer(c.customer.id, { notes }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['conversation'] }),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>{c.customer.name || c.customer.email}</CardTitle>
        {c.customer.name ? <p className="text-sm text-muted-foreground">{c.customer.email}</p> : null}
      </CardHeader>
      <CardContent className="space-y-4">
        <div>
          <p className="text-xs font-semibold mb-1">Notes about this customer</p>
          <textarea
            rows={3}
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            onBlur={() => notes !== c.customer.notes && save.mutate()}
            className="w-full rounded-md border border-input bg-background p-2 text-sm"
          />
        </div>
        <div>
          <p className="text-xs font-semibold mb-1">Previous conversations</p>
          {c.history.length === 0 ? (
            <p className="text-sm text-muted-foreground">None</p>
          ) : (
            <ul className="space-y-1">
              {c.history.map((h) => (
                <li key={h.id}>
                  <Link
                    to="/conversations/$conversationId"
                    params={{ conversationId: h.id }}
                    className="flex items-center justify-between gap-2 text-sm hover:underline"
                  >
                    <span className="truncate">{h.subject}</span>
                    <StatusBadge status={h.status} />
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

export function ConversationPage() {
  const { conversationId } = useParams({ strict: false }) as { conversationId: string };
  const qc = useQueryClient();
  const { me } = useMe();
  const [showImages, setShowImages] = useState(false);
  const q = useQuery({
    queryKey: ['conversation', conversationId],
    queryFn: ({ signal }) => getConversation(conversationId, signal),
    refetchInterval: 10_000,
  });
  const peopleQ = useQuery({ queryKey: ['people'], queryFn: ({ signal }) => listPeople(signal) });
  const update = useMutation({
    mutationFn: (body: { status?: Status; assignee_id?: string }) => updateConversation(conversationId, body),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['conversation'] });
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
    },
  });

  // Newest first, like FreeScout's default.
  const threads = useMemo(() => [...(q.data?.threads ?? [])].reverse(), [q.data?.threads]);
  const lastSeen = useRef<string | null>(null);
  const newest = q.data?.threads.filter((t) => t.kind === 'customer').at(-1)?.id ?? null;
  const [newArrived, setNewArrived] = useState(false);
  useEffect(() => {
    if (lastSeen.current && newest && newest !== lastSeen.current) setNewArrived(true);
    lastSeen.current = newest;
  }, [newest]);

  if (q.isLoading) return <LoadingRow />;
  if (q.isError || !q.data) return <ErrorRow message="Could not load this conversation." />;
  const c = q.data;
  const replying = c.viewers.filter((v) => v.mode === 'replying');
  const viewing = c.viewers.filter((v) => v.mode === 'viewing');

  return (
    <PageContainer>
      <Link
        to="/inbox"
        search={{ mailbox: c.mailbox_id, folder: 'unassigned' }}
        className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground mb-3"
      >
        <ArrowLeft className="w-4 h-4" />
        {c.mailbox_name}
      </Link>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between mb-4">
        <div>
          <h1 className="font-heading text-2xl font-extrabold tracking-tight">{c.subject}</h1>
          <p className="text-sm text-muted-foreground mt-1">
            <span className="font-mono">#{c.number}</span> · {c.mailbox_name} · opened {timeAgo(c.created_at)}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <StatusBadge status={c.status} />
          <select
            aria-label="Status"
            className={selectClass}
            value={c.status}
            onChange={(e) => update.mutate({ status: e.target.value as Status })}
          >
            {(Object.keys(STATUS_LABELS) as Status[]).map((s) => (
              <option key={s} value={s}>
                {STATUS_LABELS[s]}
              </option>
            ))}
          </select>
          <select
            aria-label="Assignee"
            className={selectClass}
            value={c.assignee_id ?? ''}
            onChange={(e) => update.mutate({ assignee_id: e.target.value })}
          >
            <option value="">Unassigned</option>
            {(peopleQ.data?.people ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.id === me?.id ? `${p.display_name} (me)` : p.display_name}
              </option>
            ))}
          </select>
          {me && c.assignee_id !== me.id ? (
            <Button size="sm" variant="secondary" onClick={() => update.mutate({ assignee_id: me.id })}>
              Take it
            </Button>
          ) : null}
        </div>
      </div>

      {replying.length > 0 ? (
        <div className="mb-4 flex items-center gap-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm">
          <AlertTriangle className="w-4 h-4 text-warning" />
          {replying.map((v) => v.display_name).join(', ')} {replying.length === 1 ? 'is' : 'are'} replying right now.
        </div>
      ) : viewing.length > 0 ? (
        <div className="mb-4 flex items-center gap-2 text-sm text-muted-foreground">
          <Eye className="w-4 h-4" />
          {viewing.map((v) => v.display_name).join(', ')} also {viewing.length === 1 ? 'has' : 'have'} this open.
        </div>
      ) : null}
      {newArrived ? (
        <div className="mb-4 flex items-center justify-between rounded-md border border-primary/40 bg-primary-soft px-3 py-2 text-sm">
          The customer wrote again while you had this open.
          <Button size="sm" variant="ghost" onClick={() => setNewArrived(false)}>
            Got it
          </Button>
        </div>
      ) : null}

      <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
        <div className="space-y-4 min-w-0">
          <Composer c={c} />
          <div className="flex justify-end">
            <Button size="sm" variant="ghost" onClick={() => setShowImages(!showImages)}>
              {showImages ? <ImageOff className="w-4 h-4" /> : <Image className="w-4 h-4" />}
              {showImages ? 'Hide remote images' : 'Show remote images'}
            </Button>
          </div>
          {threads.map((t) => (
            <ThreadCard key={t.id} t={t} showImages={showImages} />
          ))}
        </div>
        <aside>
          <CustomerCard c={c} />
        </aside>
      </div>
    </PageContainer>
  );
}
