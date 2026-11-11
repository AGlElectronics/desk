import { useEffect, useRef, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import { Inbox, Mail, PenSquare, Search, Star, X } from 'lucide-react';
import {
  createConversation,
  listConversations,
  listMailboxes,
  listTags,
  type Conversation,
  type Folder,
  type FolderCounts,
  type Mailbox,
} from '@/api/desk';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { StatusBadge } from '@/components/StatusBadge';
import { LazyRichEditor, type RichEditorHandle } from '@/components/LazyRichEditor';
import { useMe } from '@/hooks/useMe';
import { useShortcuts } from '@/hooks/useShortcuts';
import { timeAgo } from '@/lib/format';
import { cn } from '@/lib/utils';
import type { InboxSearch } from '@/router';

type FolderDef = { key: Folder; label: string; count?: keyof FolderCounts; needsDelete?: boolean };

const FOLDERS: FolderDef[] = [
  { key: 'unassigned', label: 'Unassigned', count: 'unassigned' },
  { key: 'mine', label: 'Mine', count: 'mine' },
  { key: 'assigned', label: 'Assigned', count: 'assigned' },
  { key: 'pending', label: 'Pending', count: 'pending' },
  { key: 'starred', label: 'Starred', count: 'starred' },
  { key: 'closed', label: 'Closed' },
  { key: 'spam', label: 'Spam' },
  { key: 'all', label: 'All' },
  { key: 'trash', label: 'Trash', count: 'trash', needsDelete: true },
];

function MailboxNav({ mailboxes, current }: { mailboxes: Mailbox[]; current: InboxSearch }) {
  const total = (k: keyof FolderCounts) => mailboxes.reduce((n, m) => n + m.counts[k], 0);
  const anyDelete = mailboxes.some((m) => m.access.delete);
  const item = (mailbox: string | undefined, f: FolderDef, count?: number) => {
    const active = current.mailbox === mailbox && current.folder === f.key;
    return (
      <Link
        key={`${mailbox ?? 'all'}-${f.key}`}
        to="/inbox"
        search={{ mailbox, folder: f.key, q: current.q, tag: current.tag }}
        className={cn(
          'flex items-center justify-between px-3 py-1.5 rounded-pill text-sm transition-colors',
          active ? 'bg-accent text-accent-foreground font-semibold' : 'text-muted-foreground hover:bg-secondary hover:text-foreground'
        )}
      >
        <span>{f.label}</span>
        {count ? <span className="text-xs font-semibold">{count}</span> : null}
      </Link>
    );
  };
  return (
    <div className="space-y-5">
      <div>
        <p className="px-3 mb-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground">All mailboxes</p>
        {FOLDERS.filter((f) => f.key !== 'spam' && (!f.needsDelete || anyDelete)).map((f) => item(undefined, f, f.count ? total(f.count) : undefined))}
      </div>
      {mailboxes.map((m) => (
        <div key={m.id}>
          <p className="px-3 mb-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground truncate" title={m.email}>
            {m.name}
          </p>
          {FOLDERS.filter((f) => !f.needsDelete || m.access.delete).map((f) => item(m.id, f, f.count ? m.counts[f.count] : undefined))}
          {'last_fetch_error' in m && m.last_fetch_error ? (
            <p className="px-3 mt-1 text-[11px] text-destructive" title={m.last_fetch_error}>
              Fetching mail fails
            </p>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function NewConversation({ mailboxes, onClose }: { mailboxes: Mailbox[]; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const editor = useRef<RichEditorHandle>(null);
  const [empty, setEmpty] = useState(true);
  const [form, setForm] = useState({ mailbox_id: mailboxes[0]?.id ?? '', customer_email: '', customer_name: '', subject: '' });
  const create = useMutation({
    mutationFn: () => createConversation({ ...form, body_html: editor.current?.getHTML() ?? '' }),
    onSuccess: (c) => {
      void qc.invalidateQueries({ queryKey: ['conversations'] });
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
      void navigate({ to: '/conversations/$conversationId', params: { conversationId: c.id } });
    },
  });
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setForm({ ...form, [k]: e.target.value });
  return (
    <Card className="p-5 mb-6">
      <div className="flex items-center justify-between mb-4">
        <h2 className="font-heading font-bold">New conversation</h2>
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
          <X className="w-4 h-4" />
        </Button>
      </div>
      <form
        className="grid gap-3 sm:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          create.mutate();
        }}
      >
        <div>
          <Label htmlFor="nc-mailbox">From mailbox</Label>
          <select id="nc-mailbox" className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={form.mailbox_id} onChange={set('mailbox_id')}>
            {mailboxes.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name} &lt;{m.email}&gt;
              </option>
            ))}
          </select>
        </div>
        <div>
          <Label htmlFor="nc-email">Customer email</Label>
          <Input id="nc-email" type="email" required value={form.customer_email} onChange={set('customer_email')} />
        </div>
        <div>
          <Label htmlFor="nc-name">Customer name (optional)</Label>
          <Input id="nc-name" value={form.customer_name} onChange={set('customer_name')} />
        </div>
        <div>
          <Label htmlFor="nc-subject">Subject</Label>
          <Input id="nc-subject" required value={form.subject} onChange={set('subject')} />
        </div>
        <div className="sm:col-span-2">
          <Label>Message</Label>
          <LazyRichEditor ref={editor} ariaLabel="Message" placeholder="Write your message…" onChange={setEmpty} />
        </div>
        <div className="sm:col-span-2 flex items-center gap-3">
          <Button type="submit" disabled={create.isPending || !form.mailbox_id || empty}>
            Send
          </Button>
          {create.isError ? <span className="text-sm text-destructive">{(create.error as Error).message}</span> : null}
        </div>
      </form>
    </Card>
  );
}

export function ConversationRow({ c, selected }: { c: Conversation; selected?: boolean }) {
  return (
    <Link
      to="/conversations/$conversationId"
      params={{ conversationId: c.id }}
      aria-current={selected ? 'true' : undefined}
      className={cn('flex gap-4 px-4 py-3 hover:bg-secondary/60 transition-colors', selected && 'bg-secondary ring-1 ring-inset ring-primary/40')}
    >
      <div className="w-44 shrink-0 min-w-0">
        <p className="text-sm font-semibold truncate">{c.customer.name || c.customer.email}</p>
        {c.customer.name ? <p className="text-xs text-muted-foreground truncate">{c.customer.email}</p> : null}
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm truncate flex items-center gap-1.5">
          {c.starred ? <Star aria-label="Starred" className="w-3.5 h-3.5 shrink-0 fill-warning text-warning" /> : null}
          <span className="font-semibold truncate">{c.subject}</span>
          {c.thread_count > 1 ? <span className="text-muted-foreground"> ({c.thread_count})</span> : null}
        </p>
        <p className="text-xs text-muted-foreground truncate">{c.preview}</p>
        {c.tags.length > 0 ? (
          <p className="mt-1 flex flex-wrap gap-1">
            {c.tags.map((t) => (
              <span key={t} className="rounded-pill bg-secondary px-1.5 text-[11px]">
                {t}
              </span>
            ))}
          </p>
        ) : null}
      </div>
      <div className="hidden sm:flex flex-col items-end gap-1 shrink-0 text-xs text-muted-foreground">
        <span>{timeAgo(c.last_activity_at)}</span>
        <span className="flex items-center gap-2">
          {c.assignee_name ? <span>{c.assignee_name}</span> : null}
          <StatusBadge status={c.status} />
          <span className="font-mono">#{c.number}</span>
        </span>
      </div>
    </Link>
  );
}

/** Mailboxes and folders on the left, the conversation list on the right. */
export function InboxPage() {
  const raw = useSearch({ strict: false }) as InboxSearch;
  const search = { ...raw, folder: raw.folder ?? ('unassigned' as Folder) };
  const navigate = useNavigate();
  const { isAdmin } = useMe();
  const [composing, setComposing] = useState(false);
  const [q, setQ] = useState(search.q ?? '');
  const [selected, setSelected] = useState(0);
  const searchInput = useRef<HTMLInputElement>(null);

  const boxesQ = useQuery({ queryKey: ['mailboxes'], queryFn: ({ signal }) => listMailboxes(signal), refetchInterval: 15_000 });
  const tagsQ = useQuery({ queryKey: ['tags', search.mailbox ?? 'all'], queryFn: ({ signal }) => listTags(search.mailbox, signal) });
  const listQ = useInfiniteQuery({
    queryKey: ['conversations', search],
    queryFn: ({ signal, pageParam }) =>
      listConversations({ mailbox: search.mailbox, folder: search.folder, q: search.q, tag: search.tag, cursor: pageParam || undefined }, signal),
    initialPageParam: '',
    getNextPageParam: (last) => last.next_cursor || undefined,
    refetchInterval: 15_000,
  });

  const mailboxes = boxesQ.data?.mailboxes ?? [];
  const writable = mailboxes.filter((m) => m.access.work);
  const current = mailboxes.find((m) => m.id === search.mailbox);
  const folderLabel = FOLDERS.find((f) => f.key === search.folder)?.label ?? '';
  const rows = listQ.data?.pages.flatMap((p) => p.conversations) ?? [];
  useEffect(() => setSelected(0), [search.mailbox, search.folder, search.q, search.tag]);

  useShortcuts({
    j: () => setSelected((i) => Math.min(i + 1, Math.max(rows.length - 1, 0))),
    k: () => setSelected((i) => Math.max(i - 1, 0)),
    Enter: () => rows[selected] && void navigate({ to: '/conversations/$conversationId', params: { conversationId: rows[selected].id } }),
    '/': () => searchInput.current?.focus(),
    c: () => writable.length > 0 && setComposing(true),
    Escape: () => {
      setComposing(false);
      searchInput.current?.blur();
    },
  });

  if (boxesQ.isSuccess && mailboxes.length === 0) {
    return (
      <PageContainer>
        <PageHeader title="Inbox" />
        <EmptyState
          icon={Mail}
          title={isAdmin ? 'No mailboxes yet' : 'No mailbox for you yet'}
          description={
            isAdmin
              ? 'Desk turns email sent to a support address into conversations. Add the first mailbox to start.'
              : 'Ask an administrator for a mailbox role (viewer, manager or delete) in HR.'
          }
          action={isAdmin ? <LinkButton to="/mailboxes">Add a mailbox</LinkButton> : undefined}
        />
      </PageContainer>
    );
  }

  return (
    <PageContainer className="flex flex-col lg:flex-row gap-6">
      <aside className="lg:w-56 shrink-0">{boxesQ.isLoading ? <LoadingRow /> : <MailboxNav mailboxes={mailboxes} current={search} />}</aside>
      <section className="flex-1 min-w-0">
        <PageHeader
          title={`${current ? current.name : 'All mailboxes'} · ${folderLabel}`}
          description={current?.email}
          actions={
            writable.length > 0 ? (
              <Button onClick={() => setComposing(true)}>
                <PenSquare className="w-4 h-4" />
                New conversation
              </Button>
            ) : undefined
          }
        />
        {composing ? <NewConversation mailboxes={writable} onClose={() => setComposing(false)} /> : null}
        <div className="mb-4 flex flex-wrap items-center gap-3">
          <form
            className="relative flex-1 min-w-[16rem] max-w-md"
            onSubmit={(e) => {
              e.preventDefault();
              void navigate({ to: '/inbox', search: { ...search, q: q || undefined } });
            }}
          >
            <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
            <Input ref={searchInput} className="pl-9" placeholder="Search subject, text, customer or #number ( / )" value={q} onChange={(e) => setQ(e.target.value)} />
          </form>
          {(tagsQ.data?.tags.length ?? 0) > 0 ? (
            <select
              aria-label="Filter by tag"
              className="h-9 rounded-md border border-input bg-background px-2 text-sm"
              value={search.tag ?? ''}
              onChange={(e) => void navigate({ to: '/inbox', search: { ...search, tag: e.target.value || undefined } })}
            >
              <option value="">All tags</option>
              {tagsQ.data!.tags.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          ) : null}
        </div>

        {listQ.isLoading ? (
          <LoadingRow />
        ) : listQ.isError ? (
          <ErrorRow message="Could not load conversations." />
        ) : rows.length === 0 ? (
          <EmptyState icon={Inbox} title="Nothing here" description="No conversations in this folder." />
        ) : (
          <>
            <Card className="divide-y divide-border overflow-hidden">
              {rows.map((c, i) => (
                <ConversationRow key={c.id} c={c} selected={i === selected} />
              ))}
            </Card>
            {listQ.hasNextPage ? (
              <div className="mt-3 flex justify-center">
                <Button variant="secondary" onClick={() => void listQ.fetchNextPage()} disabled={listQ.isFetchingNextPage}>
                  {listQ.isFetchingNextPage ? 'Loading…' : 'Load more'}
                </Button>
              </div>
            ) : null}
          </>
        )}
      </section>
    </PageContainer>
  );
}
