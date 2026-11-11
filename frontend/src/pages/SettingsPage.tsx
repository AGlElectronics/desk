import { useEffect, useState } from 'react';
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { DenConnection } from '@/components/DenConnection';
import { ApiError } from '@/api/client';
import {
  getAudit,
  getIntegrations,
  getNotifications,
  getRetention,
  putNotifications,
  putRetention,
  type Retention,
} from '@/api/desk';
import { PERMS, useMe } from '@/hooks/useMe';
import { dateTime, timeAgo } from '@/lib/format';

const RETENTION_FIELDS: { key: keyof Retention; label: string; help: string }[] = [
  { key: 'closed_conversation_days', label: 'Closed conversations', help: 'Deleted with their emails and files this many days after closing.' },
  { key: 'attachment_days', label: 'Attachments', help: 'Deleted this many days after they arrived.' },
  { key: 'raw_mail_days', label: 'Raw emails (.eml copies)', help: 'Deleted this many days after they arrived; the conversation stays.' },
  { key: 'trash_days', label: 'Trash', help: 'Deleted emails and conversations are purged after this many days (default 30).' },
  { key: 'mailbox_trash_days', label: 'Deleted mailboxes', help: 'Purged this many days after deletion (default 30); admins get reminders 7 days and 1 day before.' },
];

function Notifications() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['notifications'], queryFn: ({ signal }) => getNotifications(signal) });
  const save = useMutation({ mutationFn: putNotifications, onSuccess: (d) => qc.setQueryData(['notifications'], d) });
  const p = q.data;
  if (!p) return null;
  const toggle = (k: 'new_conversation' | 'assigned_to_me') => save.mutate({ new_conversation: p.new_conversation, assigned_to_me: p.assigned_to_me, [k]: !p[k] });
  return (
    <Card>
      <CardHeader>
        <CardTitle>Email notifications</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2 text-sm">
        {!p.available ? <p className="text-warning">Desk cannot send email yet (NOTIFY_FROM is not set); your choice is kept for later.</p> : null}
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={p.new_conversation} onChange={() => toggle('new_conversation')} />
          New conversations in my mailboxes
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={p.assigned_to_me} onChange={() => toggle('assigned_to_me')} />
          Someone assigns a conversation to me
        </label>
      </CardContent>
    </Card>
  );
}

function RetentionSettings() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['retention'], queryFn: ({ signal }) => getRetention(signal) });
  const [draft, setDraft] = useState<Record<string, string>>({});
  useEffect(() => {
    if (!q.data) return;
    setDraft(Object.fromEntries(RETENTION_FIELDS.map((f) => [f.key, q.data!.desk[f.key] == null ? '' : String(q.data!.desk[f.key])])));
  }, [q.data]);
  const save = useMutation({
    mutationFn: () => {
      const body: Partial<Retention> = {};
      for (const f of RETENTION_FIELDS) {
        if (q.data?.managed_by[f.key]) continue;
        body[f.key] = draft[f.key] ? Number(draft[f.key]) : null;
      }
      return putRetention(body);
    },
    onSuccess: (d) => qc.setQueryData(['retention'], d),
  });
  const v = q.data;
  if (!v) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Retention</CardTitle>
        <p className="text-sm text-muted-foreground">
          Empty means keep. Conversations, attachments and raw emails are kept until someone decides (pending legal review). Values set in Den's module settings win and are shown read-only.
        </p>
      </CardHeader>
      <CardContent className="space-y-3">
        {RETENTION_FIELDS.map((f) => {
          const den = v.managed_by[f.key] === 'den';
          const pending = v.pending_legal_review.includes(f.key);
          return (
            <div key={f.key} className="grid gap-1 sm:grid-cols-[14rem_8rem_1fr] sm:items-center">
              <Label htmlFor={`ret-${f.key}`} className="mb-0">
                {f.label}
              </Label>
              <Input
                id={`ret-${f.key}`}
                type="number"
                min={1}
                max={3650}
                placeholder="keep"
                disabled={den}
                value={den ? String(v.effective[f.key] ?? '') : (draft[f.key] ?? '')}
                onChange={(e) => setDraft({ ...draft, [f.key]: e.target.value })}
              />
              <p className="text-xs text-muted-foreground">
                {f.help}
                {den ? ' Set in Den.' : ''}
                {pending ? ' Pending legal review.' : ''}
              </p>
            </div>
          );
        })}
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => save.mutate()} disabled={save.isPending}>
            Save
          </Button>
          {save.isError ? <span className="text-sm text-destructive">{save.error instanceof ApiError ? Object.values(save.error.fields()).join('; ') || save.error.message : ''}</span> : null}
        </div>
      </CardContent>
    </Card>
  );
}

function IntegrationsCard() {
  const q = useQuery({ queryKey: ['integrations'], queryFn: ({ signal }) => getIntegrations(signal) });
  const d = q.data;
  if (!d) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>Mail connectors</CardTitle>
      </CardHeader>
      <CardContent className="text-sm space-y-1">
        {d.google ? (
          <>
            <p>
              Google Workspace <span className="font-mono">{d.google.domain}</span>, shared account <span className="font-mono">{d.google.shared_mailbox || '—'}</span>, label{' '}
              <span className="font-mono">{d.google.label}</span>.
            </p>
            <p className="text-muted-foreground">
              Shared account last read {d.google.shared_status.last_fetch_at ? timeAgo(d.google.shared_status.last_fetch_at) : 'never'}
              {d.google.shared_status.unrouted ? ` · ${d.google.shared_status.unrouted} messages for no known mailbox (label ${d.google.label}/Unrouted)` : ''}
            </p>
            {d.google.shared_status.error ? <p className="text-destructive">{d.google.shared_status.error}</p> : null}
          </>
        ) : (
          <p>Google Workspace is not configured; mailboxes use IMAP and SMTP.</p>
        )}
        <p className="text-muted-foreground">
          Desk's own emails: {d.notifications ? `from ${d.notify_from}` : 'off (set NOTIFY_FROM and SMTP_HOST or GOOGLE_SHARED_MAILBOX)'}
        </p>
      </CardContent>
    </Card>
  );
}

function AuditLog() {
  const q = useInfiniteQuery({
    queryKey: ['audit'],
    queryFn: ({ signal, pageParam }) => getAudit({ before: pageParam || undefined }, signal),
    initialPageParam: 0,
    getNextPageParam: (last) => (last.entries.length === 100 ? last.entries[last.entries.length - 1].id : undefined),
  });
  const rows = q.data?.pages.flatMap((p) => p.entries) ?? [];
  return (
    <Card>
      <CardHeader>
        <CardTitle>Audit log</CardTitle>
      </CardHeader>
      <CardContent>
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nothing yet.</p>
        ) : (
          <ul className="divide-y divide-border text-sm">
            {rows.map((e) => (
              <li key={e.id} className="py-1.5 flex flex-wrap gap-x-3">
                <span className="text-muted-foreground w-40 shrink-0" title={dateTime(e.created_at)}>
                  {dateTime(e.created_at)}
                </span>
                <span className="font-semibold">{e.actor_name}</span>
                <span>{e.summary || e.action}</span>
              </li>
            ))}
          </ul>
        )}
        {q.hasNextPage ? (
          <Button size="sm" variant="secondary" className="mt-2" onClick={() => void q.fetchNextPage()}>
            Older entries
          </Button>
        ) : null}
      </CardContent>
    </Card>
  );
}

/** Module settings: notifications for everyone; the rest for Desk admins. */
export function SettingsPage() {
  const { isAdmin, can } = useMe();
  return (
    <PageContainer>
      <PageHeader title="Settings" description={isAdmin ? 'Your notifications and Desk-wide settings.' : 'Your notifications.'} />
      <div className="max-w-3xl space-y-6">
        <Notifications />
        {isAdmin ? (
          <>
            <IntegrationsCard />
            <RetentionSettings />
            <AuditLog />
          </>
        ) : null}
        {can(PERMS.settingsManage) ? <DenConnection base="/api/v1/den" /> : null}
      </div>
    </PageContainer>
  );
}
