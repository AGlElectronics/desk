import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, Mail, Plus, RefreshCw, Trash2, XCircle } from 'lucide-react';
import {
  createMailbox,
  deleteMailbox,
  fetchMailbox,
  listMailboxes,
  testMailbox,
  updateMailbox,
  type ConnectionTest,
  type Mailbox,
  type MailboxInput,
  type Security,
} from '@/api/desk';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { timeAgo } from '@/lib/format';

type Form = {
  name: string;
  email: string;
  signature: string;
  imap_host: string;
  imap_port: string;
  imap_security: Security;
  imap_username: string;
  imap_password: string;
  smtp_host: string;
  smtp_port: string;
  smtp_security: Security;
  smtp_username: string;
  smtp_password: string;
};

const EMPTY: Form = {
  name: '',
  email: '',
  signature: '--\n{agent.name}',
  imap_host: '',
  imap_port: '993',
  imap_security: 'tls',
  imap_username: '',
  imap_password: '',
  smtp_host: '',
  smtp_port: '587',
  smtp_security: 'starttls',
  smtp_username: '',
  smtp_password: '',
};

function toForm(m: Mailbox): Form {
  return {
    ...EMPTY,
    name: m.name,
    email: m.email,
    signature: m.signature,
    imap_host: m.imap_host,
    imap_port: String(m.imap_port),
    imap_security: m.imap_security,
    imap_username: m.imap_username,
    smtp_host: m.smtp_host,
    smtp_port: String(m.smtp_port),
    smtp_security: m.smtp_security,
    smtp_username: m.smtp_username,
  };
}

function toInput(f: Form, editing: Mailbox | null): MailboxInput {
  const input: MailboxInput = {
    name: f.name,
    email: f.email,
    signature: f.signature,
    imap_host: f.imap_host,
    imap_port: Number(f.imap_port) || 993,
    imap_security: f.imap_security,
    imap_username: f.imap_username,
    smtp_host: f.smtp_host,
    smtp_port: Number(f.smtp_port) || 587,
    smtp_security: f.smtp_security,
    smtp_username: f.smtp_username,
  };
  // Blank password on edit keeps the stored one.
  if (f.imap_password || !editing) input.imap_password = f.imap_password;
  if (f.smtp_password || !editing) input.smtp_password = f.smtp_password;
  return input;
}

function SecuritySelect({ id, value, onChange }: { id: string; value: Security; onChange: (v: Security) => void }) {
  return (
    <select id={id} className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={value} onChange={(e) => onChange(e.target.value as Security)}>
      <option value="tls">SSL/TLS</option>
      <option value="starttls">STARTTLS</option>
      <option value="none">None (dev only)</option>
    </select>
  );
}

function MailboxForm({ editing, onDone }: { editing: Mailbox | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Form>(editing ? toForm(editing) : EMPTY);
  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => setF({ ...f, [k]: e.target.value });
  const save = useMutation({
    mutationFn: () => (editing ? updateMailbox(editing.id, toInput(f, editing)) : createMailbox(toInput(f, null))),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
      onDone();
    },
  });
  const field = (k: keyof Form, label: string, props: React.InputHTMLAttributes<HTMLInputElement> = {}) => (
    <div>
      <Label htmlFor={`mb-${k}`}>{label}</Label>
      <Input id={`mb-${k}`} value={f[k]} onChange={set(k)} {...props} />
    </div>
  );
  return (
    <Card className="mb-6">
      <CardHeader>
        <CardTitle>{editing ? `Edit ${editing.name}` : 'New mailbox'}</CardTitle>
      </CardHeader>
      <CardContent>
        <form
          className="space-y-6"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            {field('name', 'Name', { required: true, placeholder: 'Support' })}
            {field('email', 'Email address', { required: true, type: 'email', placeholder: 'support@example.org' })}
            <div className="sm:col-span-2">
              <Label htmlFor="mb-signature">Signature ({'{agent.name}'} is replaced by the person replying)</Label>
              <textarea id="mb-signature" rows={3} className="w-full rounded-md border border-input bg-background p-2 text-sm" value={f.signature} onChange={set('signature')} />
            </div>
          </div>
          <fieldset className="grid gap-3 sm:grid-cols-4">
            <legend className="font-heading font-bold text-sm mb-2">Incoming mail (IMAP)</legend>
            <div className="sm:col-span-2">{field('imap_host', 'Server', { placeholder: 'imap.example.org' })}</div>
            {field('imap_port', 'Port', { type: 'number' })}
            <div>
              <Label htmlFor="mb-imap-sec">Security</Label>
              <SecuritySelect id="mb-imap-sec" value={f.imap_security} onChange={(v) => setF({ ...f, imap_security: v })} />
            </div>
            <div className="sm:col-span-2">{field('imap_username', 'Username')}</div>
            <div className="sm:col-span-2">
              {field('imap_password', editing?.imap_password_set ? 'Password (leave blank to keep)' : 'Password', { type: 'password', autoComplete: 'new-password' })}
            </div>
          </fieldset>
          <fieldset className="grid gap-3 sm:grid-cols-4">
            <legend className="font-heading font-bold text-sm mb-1">Outgoing mail (SMTP)</legend>
            <p className="sm:col-span-4 text-xs text-muted-foreground -mt-1 mb-1">
              Leave the server empty to send through the module's default SMTP server.
            </p>
            <div className="sm:col-span-2">{field('smtp_host', 'Server', { placeholder: 'smtp.example.org' })}</div>
            {field('smtp_port', 'Port', { type: 'number' })}
            <div>
              <Label htmlFor="mb-smtp-sec">Security</Label>
              <SecuritySelect id="mb-smtp-sec" value={f.smtp_security} onChange={(v) => setF({ ...f, smtp_security: v })} />
            </div>
            <div className="sm:col-span-2">{field('smtp_username', 'Username')}</div>
            <div className="sm:col-span-2">
              {field('smtp_password', editing?.smtp_password_set ? 'Password (leave blank to keep)' : 'Password', { type: 'password', autoComplete: 'new-password' })}
            </div>
          </fieldset>
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={save.isPending}>
              {editing ? 'Save' : 'Create mailbox'}
            </Button>
            <Button type="button" variant="ghost" onClick={onDone}>
              Cancel
            </Button>
            {save.isError ? <span className="text-sm text-destructive">{(save.error as Error).message}</span> : null}
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function TestResult({ r }: { r: ConnectionTest }) {
  const row = (ok: boolean, label: string, err?: string) => (
    <p className="flex items-center gap-1.5 text-xs">
      {ok ? <CheckCircle2 className="w-4 h-4 text-success" /> : <XCircle className="w-4 h-4 text-destructive" />}
      <span className="font-semibold">{label}</span>
      {ok ? 'works' : err}
    </p>
  );
  return (
    <div className="mt-3 space-y-1">
      {row(r.imap_ok, 'IMAP', r.imap_error)}
      {row(r.smtp_ok, `SMTP${r.smtp_via ? ` (${r.smtp_via})` : ''}`, r.smtp_error)}
    </div>
  );
}

function MailboxRow({ m, onEdit }: { m: Mailbox; onEdit: () => void }) {
  const qc = useQueryClient();
  const [result, setResult] = useState<ConnectionTest | null>(null);
  const test = useMutation({ mutationFn: () => testMailbox(m.id), onSuccess: setResult });
  const fetchNow = useMutation({
    mutationFn: () => fetchMailbox(m.id),
    onSuccess: () => setTimeout(() => void qc.invalidateQueries({ queryKey: ['mailboxes'] }), 3000),
  });
  const remove = useMutation({
    mutationFn: () => deleteMailbox(m.id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['mailboxes'] }),
  });
  return (
    <Card className="p-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <p className="font-heading font-bold">{m.name}</p>
          <p className="text-sm text-muted-foreground">{m.email}</p>
          <p className="text-xs text-muted-foreground mt-1">
            {m.imap_host ? `IMAP ${m.imap_host}:${m.imap_port}` : 'No IMAP server — mail is not fetched'}
            {' · '}
            {m.smtp_host ? `SMTP ${m.smtp_host}:${m.smtp_port}` : 'Default SMTP'}
            {m.last_fetch_at ? ` · last fetch ${timeAgo(m.last_fetch_at)}` : ''}
          </p>
          {m.last_fetch_error ? <p className="text-xs text-destructive mt-1">Last fetch failed: {m.last_fetch_error}</p> : null}
          {result ? <TestResult r={result} /> : null}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="secondary" onClick={() => test.mutate()} disabled={test.isPending}>
            Test connection
          </Button>
          <Button size="sm" variant="secondary" onClick={() => fetchNow.mutate()} disabled={fetchNow.isPending || !m.imap_host}>
            <RefreshCw className="w-3.5 h-3.5" />
            Fetch now
          </Button>
          <Button size="sm" variant="secondary" onClick={onEdit}>
            Edit
          </Button>
          <Button
            size="sm"
            variant="ghost"
            aria-label="Delete mailbox"
            onClick={() => {
              if (confirm(`Delete ${m.email} and every conversation in it? This cannot be undone.`)) remove.mutate();
            }}
          >
            <Trash2 className="w-4 h-4 text-destructive" />
          </Button>
        </div>
      </div>
    </Card>
  );
}

/** Mailbox administration (mailbox.manage). */
export function MailboxesPage() {
  const q = useQuery({ queryKey: ['mailboxes'], queryFn: ({ signal }) => listMailboxes(signal) });
  const [editing, setEditing] = useState<Mailbox | null | 'new'>(null);
  const boxes = q.data?.mailboxes ?? [];
  return (
    <PageContainer className="max-w-4xl">
      <PageHeader
        title="Mailboxes"
        description="Each mailbox is a support address. Desk fetches its mail over IMAP and sends replies over SMTP."
        actions={
          editing === null ? (
            <Button onClick={() => setEditing('new')}>
              <Plus className="w-4 h-4" />
              Add mailbox
            </Button>
          ) : undefined
        }
      />
      {editing !== null ? (
        <MailboxForm key={editing === 'new' ? 'new' : editing.id} editing={editing === 'new' ? null : editing} onDone={() => setEditing(null)} />
      ) : null}
      {q.isLoading ? (
        <LoadingRow />
      ) : q.isError ? (
        <ErrorRow message="Could not load mailboxes." />
      ) : boxes.length === 0 && editing === null ? (
        <EmptyState icon={Mail} title="No mailboxes yet" description="Add the first support address to start receiving conversations." />
      ) : (
        <div className="space-y-3">
          {boxes.map((m) => (
            <MailboxRow key={m.id} m={m} onEdit={() => setEditing(m)} />
          ))}
        </div>
      )}
    </PageContainer>
  );
}
