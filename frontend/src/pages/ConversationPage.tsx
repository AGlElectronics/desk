import { useEffect, useMemo, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from '@tanstack/react-router';
import {
  AlertTriangle,
  ArrowLeft,
  Bot,
  Eye,
  Forward,
  GitMerge,
  Image,
  ImageOff,
  MoveRight,
  Paperclip,
  RotateCcw,
  Send,
  Star,
  StickyNote,
  Trash2,
  Undo2,
} from 'lucide-react';
import {
  addThread,
  deleteConversation,
  deleteThread,
  forwardConversation,
  getConversation,
  listPeople,
  listSavedReplies,
  listTags,
  mergeConversation,
  moveConversation,
  restoreConversation,
  restoreThread,
  retryReply,
  sendHeartbeat,
  setStar,
  undoReply,
  updateConversation,
  updateCustomer,
  type ConversationDetail,
  type Status,
  type Thread,
} from '@/api/desk';
import { apiDownload, ApiError } from '@/api/client';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { StatusBadge, STATUS_LABELS } from '@/components/StatusBadge';
import { EmailBody } from '@/components/EmailBody';
import { LazyRichEditor, type RichEditorHandle } from '@/components/LazyRichEditor';
import { TagEditor } from '@/components/TagEditor';
import { useMe } from '@/hooks/useMe';
import { useShortcuts } from '@/hooks/useShortcuts';
import { dateTime, fileSize, timeAgo } from '@/lib/format';
import { cn } from '@/lib/utils';
import { defaultCC, splitAddresses } from '@/lib/compose';

const selectClass = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

function errorText(e: unknown): string {
  if (e instanceof ApiError) {
    const fields = Object.values(e.fields());
    return fields.length ? `${e.message}: ${fields.join('; ')}` : e.message;
  }
  return (e as Error)?.message ?? 'Something went wrong';
}

function ThreadCard({ t, showImages, canDelete, myId }: { t: Thread; showImages: boolean; canDelete: boolean; myId?: string }) {
  const qc = useQueryClient();
  const refresh = () => qc.invalidateQueries({ queryKey: ['conversation'] });
  const undo = useMutation({ mutationFn: () => undoReply(t.id), onSettled: refresh });
  const retry = useMutation({ mutationFn: () => retryReply(t.id), onSettled: refresh });
  const remove = useMutation({ mutationFn: () => deleteThread(t.id), onSettled: refresh });
  const restore = useMutation({ mutationFn: () => restoreThread(t.id), onSettled: refresh });

  if (t.kind === 'system') {
    return (
      <p className="text-xs text-muted-foreground text-center py-1">
        {t.author_name ? `${t.author_name}: ` : ''}
        {t.body_text} · {timeAgo(t.created_at)}
      </p>
    );
  }
  const who = t.kind === 'customer' ? t.from_email : t.kind === 'auto_reply' ? 'Auto-reply' : t.author_name || 'Agent';
  const verb =
    t.kind === 'customer'
      ? t.auto_submitted
        ? 'sent an automatic message'
        : 'wrote'
      : t.kind === 'note'
        ? 'added a note'
        : t.kind === 'forward'
          ? `forwarded to ${t.to.join(', ')}`
          : `replied to ${[...t.to, ...t.cc].join(', ')}`;
  const sending = t.send_status === 'queued' || t.send_status === 'sending';
  return (
    <Card
      className={cn(
        'overflow-hidden',
        t.kind === 'note' && 'border-warning/40 bg-warning/5',
        (t.kind === 'reply' || t.kind === 'forward') && 'border-primary/30',
        t.deleted_at && 'opacity-60'
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-2 px-4 py-2 border-b border-border text-sm">
        <span className="flex items-center gap-2">
          {t.kind === 'note' ? <StickyNote className="w-4 h-4 text-warning" /> : null}
          {t.auto_submitted || t.kind === 'auto_reply' ? <Bot className="w-4 h-4 text-muted-foreground" /> : null}
          <span className="font-semibold">{who}</span>
          <span className="text-muted-foreground">{verb}</span>
          {t.deleted_at ? <span className="text-xs text-destructive">in the trash</span> : null}
        </span>
        <span className="flex items-center gap-2 text-xs text-muted-foreground">
          {sending ? (
            <>
              <span>Sending…</span>
              {t.author_id === myId && t.next_attempt_at && new Date(t.next_attempt_at) > new Date() && !t.send_error ? (
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
          {canDelete && !sending ? (
            t.deleted_at ? (
              <Button size="sm" variant="outline" onClick={() => restore.mutate()} disabled={restore.isPending}>
                Restore
              </Button>
            ) : (
              <Button
                size="sm"
                variant="ghost"
                aria-label="Delete this email"
                onClick={() => confirm('Move this email to the trash? It is also moved to the mailbox provider’s trash.') && remove.mutate()}
              >
                <Trash2 className="w-3.5 h-3.5" />
              </Button>
            )
          ) : null}
          <span title={dateTime(t.created_at)}>{timeAgo(t.created_at)}</span>
        </span>
      </div>
      <div className="p-4">
        {t.kind === 'customer' ? (
          <EmailBody html={t.body_html} showImages={showImages} />
        ) : (
          <div className="prose prose-sm max-w-none text-sm [&_p]:my-1" dangerouslySetInnerHTML={{ __html: t.body_html }} />
        )}
        {sending && t.send_error ? (
          <p className="mt-2 text-xs text-destructive">Last attempt failed: {t.send_error}. Retrying automatically.</p>
        ) : null}
        {remove.data?.warning || restore.data?.warning ? (
          <p className="mt-2 text-xs text-warning">{remove.data?.warning ?? restore.data?.warning}</p>
        ) : null}
        {t.attachments.filter((a) => !a.inline).length > 0 ? (
          <div className="mt-3 flex flex-wrap gap-2">
            {t.attachments
              .filter((a) => !a.inline)
              .map((a) => (
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

function Composer({ c, mailboxEmail, kind, setKind }: { c: ConversationDetail; mailboxEmail: string; kind: 'reply' | 'note'; setKind: (k: 'reply' | 'note') => void }) {
  const qc = useQueryClient();
  const editor = useRef<RichEditorHandle>(null);
  const [empty, setEmpty] = useState(true);
  const [cc, setCC] = useState(() => defaultCC(c, mailboxEmail).join(', '));
  const [after, setAfter] = useState<Status>('pending');
  const saved = useQuery({ queryKey: ['saved-replies', c.mailbox_id], queryFn: ({ signal }) => listSavedReplies(c.mailbox_id, signal) });

  // Heartbeat: others see "viewing" or "replying" while this page is open.
  useEffect(() => {
    const beat = () => void sendHeartbeat(c.id, empty ? 'viewing' : 'replying').catch(() => undefined);
    beat();
    const t = setInterval(beat, 10_000);
    return () => clearInterval(t);
  }, [c.id, empty]);

  const send = useMutation({
    mutationFn: () =>
      addThread(c.id, {
        kind,
        body_html: editor.current?.getHTML() ?? '',
        cc: kind === 'reply' ? splitAddresses(cc) : undefined,
        status: kind === 'reply' ? after : undefined,
      }),
    onSuccess: () => {
      editor.current?.clear();
      setEmpty(true);
      void qc.invalidateQueries({ queryKey: ['conversation'] });
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
    },
  });

  return (
    <Card className={cn('p-4', kind === 'note' && 'border-warning/40 bg-warning/5')}>
      <div className="flex flex-wrap gap-1 mb-3 items-center">
        {(['reply', 'note'] as const).map((k) => (
          <button
            key={k}
            type="button"
            onClick={() => setKind(k)}
            className={cn('px-3 py-1.5 rounded-pill text-sm font-medium', kind === k ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-secondary')}
          >
            {k === 'reply' ? 'Reply' : 'Note'}
          </button>
        ))}
        {(saved.data?.saved_replies.length ?? 0) > 0 ? (
          <select
            aria-label="Insert saved reply"
            className={cn(selectClass, 'ml-2 h-8')}
            value=""
            onChange={(e) => {
              const r = saved.data?.saved_replies.find((x) => x.id === e.target.value);
              if (r) {
                editor.current?.appendHTML(r.body_html);
                setEmpty(false);
              }
            }}
          >
            <option value="">Insert saved reply…</option>
            {saved.data!.saved_replies.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
              </option>
            ))}
          </select>
        ) : null}
        <span className="ml-auto text-xs text-muted-foreground">{kind === 'reply' ? `To ${c.customer.email}` : 'Only visible to the team'}</span>
      </div>
      {kind === 'reply' ? (
        <div className="mb-2 flex items-center gap-2">
          <Label htmlFor="composer-cc" className="text-xs w-6 mb-0">
            Cc
          </Label>
          <Input id="composer-cc" className="h-8 text-xs" value={cc} onChange={(e) => setCC(e.target.value)} placeholder="Nobody" />
        </div>
      ) : null}
      <LazyRichEditor
        ref={editor}
        ariaLabel={kind === 'reply' ? 'Reply' : 'Note'}
        placeholder={kind === 'reply' ? 'Write your reply…' : 'Write an internal note…'}
        onChange={setEmpty}
      />
      <div className="mt-3 flex flex-wrap items-center gap-3">
        <Button onClick={() => send.mutate()} disabled={empty || send.isPending}>
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
        {send.isError ? <span className="text-sm text-destructive">{errorText(send.error)}</span> : null}
      </div>
    </Card>
  );
}

function ForwardPanel({ c, onClose }: { c: ConversationDetail; onClose: () => void }) {
  const qc = useQueryClient();
  const editor = useRef<RichEditorHandle>(null);
  const [to, setTo] = useState('');
  const send = useMutation({
    mutationFn: () => forwardConversation(c.id, { to: splitAddresses(to), body_html: editor.current?.getHTML() }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['conversation'] });
      onClose();
    },
  });
  return (
    <Card className="p-4 space-y-3">
      <p className="text-sm font-semibold">Forward the customer’s latest message (with its attachments)</p>
      <div>
        <Label htmlFor="fw-to">To</Label>
        <Input id="fw-to" value={to} onChange={(e) => setTo(e.target.value)} placeholder="colleague@example.org" />
      </div>
      <LazyRichEditor ref={editor} placeholder="Add a message (optional)" ariaLabel="Forward message" />
      <div className="flex items-center gap-2">
        <Button onClick={() => send.mutate()} disabled={!to.trim() || send.isPending}>
          <Forward className="w-4 h-4" />
          Forward
        </Button>
        <Button variant="ghost" onClick={onClose}>
          Cancel
        </Button>
        {send.isError ? <span className="text-sm text-destructive">{errorText(send.error)}</span> : null}
      </div>
    </Card>
  );
}

function CustomerCard({ c, canWork }: { c: ConversationDetail; canWork: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [notes, setNotes] = useState(c.customer.notes);
  useEffect(() => setNotes(c.customer.notes), [c.customer.id, c.customer.notes]);
  const save = useMutation({
    mutationFn: () => updateCustomer(c.customer.id, { notes }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['conversation'] }),
  });
  const merge = useMutation({
    mutationFn: (into: string) => mergeConversation(c.id, into),
    onSuccess: (dst) => void navigate({ to: '/conversations/$conversationId', params: { conversationId: dst.id } }),
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <Link to="/customers/$customerId" params={{ customerId: c.customer.id }} className="hover:underline">
            {c.customer.name || c.customer.email}
          </Link>
        </CardTitle>
        {c.customer.name ? <p className="text-sm text-muted-foreground">{c.customer.email}</p> : null}
      </CardHeader>
      <CardContent className="space-y-4">
        <div>
          <p className="text-xs font-semibold mb-1">Notes about this customer</p>
          <textarea
            rows={3}
            value={notes}
            readOnly={!canWork}
            onChange={(e) => setNotes(e.target.value)}
            onBlur={() => canWork && notes !== c.customer.notes && save.mutate()}
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
                <li key={h.id} className="flex items-center justify-between gap-2">
                  <Link to="/conversations/$conversationId" params={{ conversationId: h.id }} className="flex-1 min-w-0 flex items-center justify-between gap-2 text-sm hover:underline">
                    <span className="truncate">
                      #{h.number} {h.subject}
                    </span>
                    <StatusBadge status={h.status} />
                  </Link>
                  {canWork ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={`Merge into #${h.number}`}
                      title={`Merge this conversation into #${h.number}`}
                      onClick={() => confirm(`Move every message of #${c.number} into #${h.number}? #${c.number} is removed.`) && merge.mutate(h.id)}
                    >
                      <GitMerge className="w-3.5 h-3.5" />
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
          {merge.isError ? <p className="text-xs text-destructive mt-1">{errorText(merge.error)}</p> : null}
        </div>
      </CardContent>
    </Card>
  );
}

export function ConversationPage() {
  const { conversationId } = useParams({ strict: false }) as { conversationId: string };
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { me } = useMe();
  const [showImages, setShowImages] = useState(false);
  const [kind, setKind] = useState<'reply' | 'note'>('reply');
  const [panel, setPanel] = useState<'none' | 'forward' | 'move'>('none');
  const q = useQuery({
    queryKey: ['conversation', conversationId],
    queryFn: ({ signal }) => getConversation(conversationId, signal),
    refetchInterval: 10_000,
  });
  const c = q.data;
  const rights = c?.access ?? { view: false, work: false, delete: false, admin: false };
  const peopleQ = useQuery({
    queryKey: ['people', c?.mailbox_id],
    queryFn: ({ signal }) => listPeople(c!.mailbox_id, signal),
    enabled: !!c && rights.work,
  });
  const tagsQ = useQuery({ queryKey: ['tags', c?.mailbox_id], queryFn: ({ signal }) => listTags(c!.mailbox_id, signal), enabled: !!c && rights.work });
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ['conversation'] });
    void qc.invalidateQueries({ queryKey: ['conversations'] });
    void qc.invalidateQueries({ queryKey: ['mailboxes'] });
  };
  const update = useMutation({
    mutationFn: (body: { status?: Status; assignee_id?: string; tags?: string[] }) => updateConversation(conversationId, body),
    onSuccess: invalidate,
  });
  const star = useMutation({ mutationFn: (on: boolean) => setStar(conversationId, on), onSuccess: invalidate });
  const trash = useMutation({ mutationFn: () => deleteConversation(conversationId), onSuccess: invalidate });
  const untrash = useMutation({ mutationFn: () => restoreConversation(conversationId), onSuccess: invalidate });
  const move = useMutation({ mutationFn: (mailboxId: string) => moveConversation(conversationId, mailboxId), onSuccess: invalidate });

  // Newest first, like FreeScout's default.
  const threads = useMemo(() => [...(c?.threads ?? [])].reverse(), [c?.threads]);
  const lastSeen = useRef<string | null>(null);
  const newest = c?.threads.filter((t) => t.kind === 'customer').at(-1)?.id ?? null;
  const [newArrived, setNewArrived] = useState(false);
  useEffect(() => {
    if (lastSeen.current && newest && newest !== lastSeen.current) setNewArrived(true);
    lastSeen.current = newest;
  }, [newest]);

  const mailbox = me?.mailboxes.find((m) => m.id === c?.mailbox_id);
  const moveTargets = (me?.mailboxes ?? []).filter((m) => m.access.work && m.id !== c?.mailbox_id);

  useShortcuts(
    {
      r: () => rights.work && setKind('reply'),
      n: () => rights.work && setKind('note'),
      a: () => rights.work && me && update.mutate({ assignee_id: me.id }),
      x: () => rights.work && update.mutate({ status: 'closed' }),
      s: () => c && star.mutate(!c.starred),
      u: () => void navigate({ to: '/inbox', search: { mailbox: c?.mailbox_id, folder: 'unassigned' } }),
    },
    !!c
  );

  if (q.isLoading) return <LoadingRow />;
  if (q.isError || !c) return <ErrorRow message="Could not load this conversation." />;
  const replying = c.viewers.filter((v) => v.mode === 'replying');
  const viewing = c.viewers.filter((v) => v.mode === 'viewing');

  return (
    <PageContainer>
      <Link to="/inbox" search={{ mailbox: c.mailbox_id, folder: 'unassigned' }} className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground mb-3">
        <ArrowLeft className="w-4 h-4" />
        {c.mailbox_name}
      </Link>
      {c.deleted_at ? (
        <div className="mb-4 flex items-center justify-between rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
          This conversation is in the trash since {timeAgo(c.deleted_at)}.
          {rights.delete ? (
            <Button size="sm" variant="secondary" onClick={() => untrash.mutate()} disabled={untrash.isPending}>
              Restore
            </Button>
          ) : null}
        </div>
      ) : null}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between mb-2">
        <div>
          <h1 className="font-heading text-2xl font-extrabold tracking-tight flex items-center gap-2">
            <button type="button" aria-label={c.starred ? 'Unstar' : 'Star'} onClick={() => star.mutate(!c.starred)}>
              <Star className={cn('w-5 h-5', c.starred ? 'fill-warning text-warning' : 'text-muted-foreground')} />
            </button>
            {c.subject}
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            <span className="font-mono">#{c.number}</span> · {c.mailbox_name} · opened {timeAgo(c.created_at)}
          </p>
        </div>
        {rights.work && !c.deleted_at ? (
          <div className="flex flex-wrap items-center gap-2">
            <StatusBadge status={c.status} />
            <select aria-label="Status" className={selectClass} value={c.status} onChange={(e) => update.mutate({ status: e.target.value as Status })}>
              {(Object.keys(STATUS_LABELS) as Status[]).map((s) => (
                <option key={s} value={s}>
                  {STATUS_LABELS[s]}
                </option>
              ))}
            </select>
            <select aria-label="Assignee" className={selectClass} value={c.assignee_id ?? ''} onChange={(e) => update.mutate({ assignee_id: e.target.value })}>
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
        ) : (
          <StatusBadge status={c.status} />
        )}
      </div>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <TagEditor tags={c.tags} suggestions={tagsQ.data?.tags} disabled={!rights.work || !!c.deleted_at} onChange={(tags) => update.mutate({ tags })} />
        <span className="ml-auto flex flex-wrap gap-1">
          {rights.work && !c.deleted_at ? (
            <>
              <Button size="sm" variant="ghost" onClick={() => setPanel(panel === 'forward' ? 'none' : 'forward')}>
                <Forward className="w-3.5 h-3.5" />
                Forward
              </Button>
              {moveTargets.length > 0 ? (
                <Button size="sm" variant="ghost" onClick={() => setPanel(panel === 'move' ? 'none' : 'move')}>
                  <MoveRight className="w-3.5 h-3.5" />
                  Move
                </Button>
              ) : null}
            </>
          ) : null}
          {rights.delete && !c.deleted_at ? (
            <Button size="sm" variant="ghost" onClick={() => confirm('Move this conversation to the trash?') && trash.mutate()}>
              <Trash2 className="w-3.5 h-3.5 text-destructive" />
              Delete
            </Button>
          ) : null}
        </span>
      </div>
      {update.isError ? <p className="mb-3 text-sm text-destructive">{errorText(update.error)}</p> : null}
      {trash.data?.warning ? <p className="mb-3 text-sm text-warning">{trash.data.warning}</p> : null}
      {panel === 'move' ? (
        <Card className="p-4 mb-4 flex flex-wrap items-center gap-2">
          <span className="text-sm">Move to</span>
          {moveTargets.map((m) => (
            <Button key={m.id} size="sm" variant="secondary" onClick={() => move.mutate(m.id, { onSuccess: () => setPanel('none') })}>
              {m.name}
            </Button>
          ))}
          {move.isError ? <span className="text-sm text-destructive">{errorText(move.error)}</span> : null}
        </Card>
      ) : null}

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
          {panel === 'forward' ? <ForwardPanel c={c} onClose={() => setPanel('none')} /> : null}
          {rights.work && !c.deleted_at ? <Composer c={c} mailboxEmail={mailbox?.email ?? ''} kind={kind} setKind={setKind} /> : null}
          <div className="flex justify-end">
            <Button size="sm" variant="ghost" onClick={() => setShowImages(!showImages)}>
              {showImages ? <ImageOff className="w-4 h-4" /> : <Image className="w-4 h-4" />}
              {showImages ? 'Hide remote images' : 'Show remote images'}
            </Button>
          </div>
          {c.threads_truncated ? <p className="text-xs text-muted-foreground">Only the newest 500 messages are shown.</p> : null}
          {threads.map((t) => (
            <ThreadCard key={t.id} t={t} showImages={showImages} canDelete={rights.delete} myId={me?.id} />
          ))}
        </div>
        <aside>
          <CustomerCard c={c} canWork={rights.work} />
        </aside>
      </div>
    </PageContainer>
  );
}
