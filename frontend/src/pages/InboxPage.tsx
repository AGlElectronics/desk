import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useSearch } from '@tanstack/react-router';
import { Inbox, Mail, PenSquare, Search, X } from 'lucide-react';
import {
  createConversation,
  listConversations,
  listMailboxes,
  type Folder,
  type FolderCounts,
  type MailboxWithCounts,
} from '@/api/desk';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { StatusBadge } from '@/components/StatusBadge';
import { PERMS, useMe } from '@/hooks/useMe';
import { timeAgo } from '@/lib/format';
import { cn } from '@/lib/utils';
import type { InboxSearch } from '@/router';

const FOLDERS: { key: Folder; label: string; count?: keyof FolderCounts }[] = [
  { key: 'unassigned', label: 'Unassigned', count: 'unassigned' },
  { key: 'mine', label: 'Mine', count: 'mine' },
  { key: 'assigned', label: 'Assigned', count: 'assigned' },
  { key: 'pending', label: 'Pending', count: 'pending' },
  { key: 'closed', label: 'Closed' },
  { key: 'spam', label: 'Spam' },
  { key: 'all', label: 'All' },
];

function MailboxNav({ mailboxes, current }: { mailboxes: MailboxWithCounts[]; current: InboxSearch }) {
  const total = (k: keyof FolderCounts) => mailboxes.reduce((n, m) => n + m.counts[k], 0);
  const item = (mailbox: string | undefined, f: (typeof FOLDERS)[number], count?: number) => {
    const active = current.mailbox === mailbox && current.folder === f.key;
    return (
      <Link
        key={`${mailbox ?? 'all'}-${f.key}`}
        to="/inbox"
        search={{ mailbox, folder: f.key, q: current.q }}
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
        {FOLDERS.filter((f) => f.key !== 'spam').map((f) => item(undefined, f, f.count ? total(f.count) : undefined))}
      </div>
      {mailboxes.map((m) => (
        <div key={m.id}>
          <p className="px-3 mb-1 text-[11px] font-bold uppercase tracking-wider text-muted-foreground truncate" title={m.email}>
            {m.name}
          </p>
          {FOLDERS.map((f) => item(m.id, f, f.count ? m.counts[f.count] : undefined))}
          {m.last_fetch_error ? (
            <p className="px-3 mt-1 text-[11px] text-destructive" title={m.last_fetch_error}>
              Fetching mail fails
            </p>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function NewConversation({ mailboxes, onClose }: { mailboxes: MailboxWithCounts[]; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [form, setForm] = useState({
    mailbox_id: mailboxes[0]?.id ?? '',
    customer_email: '',
    customer_name: '',
    subject: '',
    body: '',
  });
  const create = useMutation({
    mutationFn: () => createConversation(form),
    onSuccess: (c) => {
      void qc.invalidateQueries({ queryKey: ['conversations'] });
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
      void navigate({ to: '/conversations/$conversationId', params: { conversationId: c.id } });
    },
  });
  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) =>
    setForm({ ...form, [k]: e.target.value });
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
          <Label htmlFor="nc-body">Message</Label>
          <textarea
            id="nc-body"
            required
            rows={6}
            className="w-full rounded-md border border-input bg-background p-3 text-sm"
            value={form.body}
            onChange={set('body')}
          />
        </div>
        <div className="sm:col-span-2 flex items-center gap-3">
          <Button type="submit" disabled={create.isPending || !form.mailbox_id}>
            Send
          </Button>
          {create.isError ? <span className="text-sm text-destructive">{(create.error as Error).message}</span> : null}
        </div>
      </form>
    </Card>
  );
}

/** Mailboxes and folders on the left, the conversation list on the right. */
export function InboxPage() {
  const raw = useSearch({ strict: false }) as InboxSearch;
  const search = { ...raw, folder: raw.folder ?? ('unassigned' as Folder) };
  const navigate = useNavigate();
  const { can } = useMe();
  const [composing, setComposing] = useState(false);
  const [q, setQ] = useState(search.q ?? '');

  const boxesQ = useQuery({
    queryKey: ['mailboxes'],
    queryFn: ({ signal }) => listMailboxes(signal),
    refetchInterval: 15_000,
  });
  const listQ = useQuery({
    queryKey: ['conversations', search],
    queryFn: ({ signal }) => listConversations({ mailbox: search.mailbox, folder: search.folder, q: search.q }, signal),
    refetchInterval: 15_000,
  });

  const mailboxes = boxesQ.data?.mailboxes ?? [];
  const current = mailboxes.find((m) => m.id === search.mailbox);
  const folderLabel = FOLDERS.find((f) => f.key === search.folder)?.label ?? '';

  if (boxesQ.isSuccess && mailboxes.length === 0) {
    return (
      <PageContainer>
        <PageHeader title="Inbox" />
        <EmptyState
          icon={Mail}
          title="No mailboxes yet"
          description="Desk turns email sent to a support address into conversations. Add the first mailbox to start."
          action={can(PERMS.mailboxManage) ? <LinkButton to="/mailboxes">Add a mailbox</LinkButton> : undefined}
        />
      </PageContainer>
    );
  }

  return (
    <PageContainer className="flex flex-col lg:flex-row gap-6">
      <aside className="lg:w-56 shrink-0">
        {boxesQ.isLoading ? <LoadingRow /> : <MailboxNav mailboxes={mailboxes} current={search} />}
      </aside>
      <section className="flex-1 min-w-0">
        <PageHeader
          title={`${current ? current.name : 'All mailboxes'} · ${folderLabel}`}
          description={current?.email}
          actions={
            <Button onClick={() => setComposing(true)} disabled={mailboxes.length === 0}>
              <PenSquare className="w-4 h-4" />
              New conversation
            </Button>
          }
        />
        {composing ? <NewConversation mailboxes={mailboxes} onClose={() => setComposing(false)} /> : null}
        <form
          className="relative mb-4 max-w-md"
          onSubmit={(e) => {
            e.preventDefault();
            void navigate({ to: '/inbox', search: { ...search, q: q || undefined } });
          }}
        >
          <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-9" placeholder="Search subject, customer, text or #number" value={q} onChange={(e) => setQ(e.target.value)} />
        </form>

        {listQ.isLoading ? (
          <LoadingRow />
        ) : listQ.isError ? (
          <ErrorRow message="Could not load conversations." />
        ) : (listQ.data?.conversations.length ?? 0) === 0 ? (
          <EmptyState icon={Inbox} title="Nothing here" description="No conversations in this folder." />
        ) : (
          <Card className="divide-y divide-border overflow-hidden">
            {listQ.data!.conversations.map((c) => (
              <Link
                key={c.id}
                to="/conversations/$conversationId"
                params={{ conversationId: c.id }}
                className="flex gap-4 px-4 py-3 hover:bg-secondary/60 transition-colors"
              >
                <div className="w-44 shrink-0 min-w-0">
                  <p className="text-sm font-semibold truncate">{c.customer.name || c.customer.email}</p>
                  {c.customer.name ? <p className="text-xs text-muted-foreground truncate">{c.customer.email}</p> : null}
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm truncate">
                    <span className="font-semibold">{c.subject}</span>
                    {c.thread_count > 1 ? <span className="text-muted-foreground"> ({c.thread_count})</span> : null}
                  </p>
                  <p className="text-xs text-muted-foreground truncate">{c.preview}</p>
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
            ))}
          </Card>
        )}
      </section>
    </PageContainer>
  );
}
