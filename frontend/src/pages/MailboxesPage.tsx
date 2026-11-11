import { useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Mail, Plus, RefreshCw, Trash2, XCircle } from 'lucide-react';
import {
  createMailbox,
  createSavedReply,
  deleteMailbox,
  deleteSavedReply,
  fetchMailbox,
  getIntegrations,
  isAdminView,
  listImportFailures,
  listMailboxes,
  listSavedReplies,
  listTrashedMailboxes,
  provisionMailbox,
  purgeMailbox,
  restoreMailbox,
  testMailbox,
  updateMailbox,
  type ConnectionTest,
  type Mailbox,
  type MailboxAdmin,
  type Security,
} from '@/api/desk';
import { ApiError } from '@/api/client';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { LazyRichEditor, type RichEditorHandle } from '@/components/LazyRichEditor';
import { useMe } from '@/hooks/useMe';
import { dateTime, timeAgo } from '@/lib/format';
import { CONNECTOR_LABELS, EMPTY, toForm, toInput, type Form } from '@/lib/mailboxForm';

function errorText(e: unknown) {
  if (e instanceof ApiError) {
    const fields = Object.entries(e.fields()).map(([k, v]) => `${k}: ${v}`);
    return fields.length ? `${e.message} (${fields.join('; ')})` : e.message;
  }
  return (e as Error).message;
}

function SecuritySelect({ id, value, onChange }: { id: string; value: Security; onChange: (v: Security) => void }) {
  return (
    <select id={id} className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={value} onChange={(e) => onChange(e.target.value as Security)}>
      <option value="tls">SSL/TLS</option>
      <option value="starttls">STARTTLS</option>
      <option value="none">None (development only)</option>
    </select>
  );
}

function MailboxForm({ editing, google, onDone }: { editing: MailboxAdmin | null; google: boolean; onDone: () => void }) {
  const qc = useQueryClient();
  const [f, setF] = useState<Form>(editing ? toForm(editing) : EMPTY);
  const set = (k: keyof Form) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
  const save = useMutation({
    mutationFn: () => (editing ? updateMailbox(editing.id, toInput(f, editing)) : createMailbox(toInput(f, null))),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['mailboxes'] });
      void qc.invalidateQueries({ queryKey: ['me'] });
      onDone();
    },
  });
  const field = (k: keyof Form, label: string, props: React.InputHTMLAttributes<HTMLInputElement> = {}) => (
    <div>
      <Label htmlFor={`mb-${k}`}>{label}</Label>
      <Input id={`mb-${k}`} value={f[k]} onChange={set(k)} {...props} />
    </div>
  );
  const hostChanged = editing && (f.imap_host !== editing.imap_host || f.imap_username !== editing.imap_username);
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
            {!editing ? (
              <div className="sm:col-span-2">
                <Label htmlFor="mb-connector">Where the mail lives</Label>
                <select id="mb-connector" className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm" value={f.connector} onChange={set('connector')}>
                  <option value="imap">{CONNECTOR_LABELS.imap}</option>
                  {google ? <option value="google_user">{CONNECTOR_LABELS.google_user}</option> : null}
                  {google ? <option value="google_group">{CONNECTOR_LABELS.google_group}</option> : null}
                </select>
                {f.connector !== 'imap' ? (
                  <p className="mt-1 text-xs text-muted-foreground">
                    {f.connector === 'google_user'
                      ? 'Desk creates the address as a licensed Workspace user and reads and sends as that user.'
                      : 'Desk creates a free group that delivers into the shared account, routes by recipient and replies as this address.'}
                  </p>
                ) : null}
              </div>
            ) : null}
            {field('name', 'Name', { required: true, placeholder: 'Support' })}
            {field('email', 'Email address', { required: true, type: 'email', placeholder: 'support@example.org', disabled: !!editing && editing.connector !== 'imap' })}
            {!editing ? (
              <div className="sm:col-span-2">
                {field('slug', 'Role name (optional; from the address when empty)', { placeholder: 'support', pattern: '[a-z0-9]([a-z0-9-]*[a-z0-9])?' })}
                <p className="mt-1 text-xs text-muted-foreground">Names the roles desk-{f.slug || 'slug'}-viewer, -manager and -delete. It cannot be changed later.</p>
              </div>
            ) : null}
            <div className="sm:col-span-2">
              <Label htmlFor="mb-signature">Signature ({'{agent.name}'} is replaced by the person replying)</Label>
              <textarea id="mb-signature" rows={3} className="w-full rounded-md border border-input bg-background p-2 text-sm" value={f.signature} onChange={set('signature')} />
            </div>
          </div>
          {f.connector === 'imap' ? (
            <>
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
                  {field('imap_password', editing?.imap_password_set && !hostChanged ? 'Password (leave blank to keep)' : 'Password', {
                    type: 'password',
                    autoComplete: 'new-password',
                    required: !!hostChanged && editing?.imap_password_set,
                  })}
                </div>
                <div className="sm:col-span-4">{field('imap_move_to', 'After fetching, move mail to folder (optional; otherwise it stays in INBOX marked read)', { placeholder: 'Desk' })}</div>
              </fieldset>
              <fieldset className="grid gap-3 sm:grid-cols-4">
                <legend className="font-heading font-bold text-sm mb-1">Outgoing mail (SMTP)</legend>
                <p className="sm:col-span-4 text-xs text-muted-foreground -mt-1 mb-1">Leave the server empty to send through the module's default SMTP server.</p>
                <div className="sm:col-span-2">{field('smtp_host', 'Server', { placeholder: 'smtp.example.org' })}</div>
                {field('smtp_port', 'Port', { type: 'number' })}
                <div>
                  <Label htmlFor="mb-smtp-sec">Security</Label>
                  <SecuritySelect id="mb-smtp-sec" value={f.smtp_security} onChange={(v) => setF({ ...f, smtp_security: v })} />
                </div>
                <div className="sm:col-span-2">{field('smtp_username', 'Username')}</div>
                <div className="sm:col-span-2">
                  {field('smtp_password', editing?.smtp_password_set ? 'Password (leave blank to keep; required when the server changes)' : 'Password', {
                    type: 'password',
                    autoComplete: 'new-password',
                  })}
                </div>
              </fieldset>
            </>
          ) : null}
          <div className="flex items-center gap-3">
            <Button type="submit" disabled={save.isPending}>
              {editing ? 'Save' : 'Create mailbox'}
            </Button>
            <Button type="button" variant="ghost" onClick={onDone}>
              Cancel
            </Button>
            {save.isError ? <span className="text-sm text-destructive">{errorText(save.error)}</span> : null}
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

/** Signature, auto-reply and saved replies: for the mailbox's managers. */
function ManagerSettings({ m }: { m: Mailbox }) {
  const qc = useQueryClient();
  const auto = useRef<RichEditorHandle>(null);
  const reply = useRef<RichEditorHandle>(null);
  const [signature, setSignature] = useState(m.signature);
  const [enabled, setEnabled] = useState(m.auto_reply.enabled);
  const [subject, setSubject] = useState(m.auto_reply.subject);
  const [replyName, setReplyName] = useState('');
  const saved = useQuery({ queryKey: ['saved-replies', m.id], queryFn: ({ signal }) => listSavedReplies(m.id, signal) });
  const save = useMutation({
    mutationFn: () =>
      updateMailbox(m.id, {
        signature,
        auto_reply: { enabled, subject, body_html: auto.current?.getHTML() ?? m.auto_reply.body_html, body_text: '' },
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['mailboxes'] }),
  });
  const addReply = useMutation({
    mutationFn: () => createSavedReply({ mailbox_id: m.id, name: replyName, body_html: reply.current?.getHTML() ?? '' }),
    onSuccess: () => {
      setReplyName('');
      reply.current?.clear();
      void qc.invalidateQueries({ queryKey: ['saved-replies', m.id] });
    },
  });
  const removeReply = useMutation({
    mutationFn: (id: string) => deleteSavedReply(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['saved-replies', m.id] }),
  });
  return (
    <div className="mt-4 grid gap-6 lg:grid-cols-2 border-t border-border pt-4">
      <div className="space-y-3">
        <div>
          <Label htmlFor={`sig-${m.id}`}>Signature</Label>
          <textarea id={`sig-${m.id}`} rows={3} className="w-full rounded-md border border-input bg-background p-2 text-sm" value={signature} onChange={(e) => setSignature(e.target.value)} />
        </div>
        <label className="flex items-center gap-2 text-sm font-semibold">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Auto-reply to the first message of a new conversation
        </label>
        <p className="text-xs text-muted-foreground">Never sent to automatic mail, mailing lists or no-reply senders, and at most once a day per customer.</p>
        <Input aria-label="Auto-reply subject" placeholder="Subject (default: Re: their subject)" value={subject} onChange={(e) => setSubject(e.target.value)} />
        <LazyRichEditor ref={auto} initialHTML={m.auto_reply.body_html} placeholder="We received your message and answer within one day." ariaLabel="Auto-reply text" />
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={() => save.mutate()} disabled={save.isPending}>
            Save
          </Button>
          {save.isSuccess ? <span className="text-xs text-success">Saved</span> : null}
          {save.isError ? <span className="text-xs text-destructive">{errorText(save.error)}</span> : null}
        </div>
      </div>
      <div className="space-y-3">
        <p className="text-sm font-semibold">Saved replies</p>
        <ul className="space-y-1">
          {(saved.data?.saved_replies ?? []).map((r) => (
            <li key={r.id} className="flex items-center justify-between text-sm">
              <span>
                {r.name}
                {r.mailbox_id ? '' : <span className="text-xs text-muted-foreground"> (all mailboxes)</span>}
              </span>
              {r.mailbox_id ? (
                <Button size="sm" variant="ghost" aria-label={`Delete ${r.name}`} onClick={() => removeReply.mutate(r.id)}>
                  <Trash2 className="w-3.5 h-3.5" />
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
        <Input aria-label="Saved reply name" placeholder="Name, e.g. Opening hours" value={replyName} onChange={(e) => setReplyName(e.target.value)} />
        <LazyRichEditor ref={reply} placeholder="Text of the saved reply" ariaLabel="Saved reply text" />
        <Button size="sm" variant="secondary" onClick={() => addReply.mutate()} disabled={!replyName.trim() || addReply.isPending}>
          Add saved reply
        </Button>
        {addReply.isError ? <span className="text-xs text-destructive">{errorText(addReply.error)}</span> : null}
      </div>
    </div>
  );
}

function ImportFailures({ id }: { id: string }) {
  const q = useQuery({ queryKey: ['import-failures', id], queryFn: ({ signal }) => listImportFailures(id, signal) });
  const rows = q.data?.failures ?? [];
  if (rows.length === 0) return <p className="mt-2 text-xs text-muted-foreground">No skipped messages.</p>;
  return (
    <ul className="mt-2 space-y-1 text-xs">
      {rows.map((f) => (
        <li key={f.id}>
          <span className="font-semibold">{f.subject || '(no subject)'}</span> {f.from_email ? `from ${f.from_email}` : ''} · {f.error} · {timeAgo(f.created_at)}
        </li>
      ))}
    </ul>
  );
}

function MailboxRow({ m, onEdit }: { m: Mailbox; onEdit: () => void }) {
  const qc = useQueryClient();
  const [result, setResult] = useState<ConnectionTest | null>(null);
  const [open, setOpen] = useState<'none' | 'settings' | 'failures'>('none');
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['mailboxes'] });
    void qc.invalidateQueries({ queryKey: ['me'] });
  };
  const test = useMutation({ mutationFn: () => testMailbox(m.id), onSuccess: setResult });
  const fetchNow = useMutation({ mutationFn: () => fetchMailbox(m.id), onSuccess: () => setTimeout(refresh, 3000) });
  const provision = useMutation({ mutationFn: () => provisionMailbox(m.id), onSuccess: refresh });
  const remove = useMutation({ mutationFn: () => deleteMailbox(m.id), onSuccess: refresh });
  const admin = isAdminView(m) ? m : null;
  return (
    <Card className="p-5">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="font-heading font-bold">{m.name}</p>
          <p className="text-sm text-muted-foreground">{m.email}</p>
          <p className="text-xs text-muted-foreground mt-1">{CONNECTOR_LABELS[m.connector]}</p>
          {admin && m.connector === 'imap' ? (
            <p className="text-xs text-muted-foreground mt-1">
              {admin.imap_host ? `IMAP ${admin.imap_host}:${admin.imap_port}` : 'No IMAP server — mail is not fetched'}
              {' · '}
              {admin.smtp_host ? `SMTP ${admin.smtp_host}:${admin.smtp_port}` : 'Default SMTP'}
            </p>
          ) : null}
          {admin?.last_fetch_at ? <p className="text-xs text-muted-foreground">Last fetch {timeAgo(admin.last_fetch_at)}</p> : null}
          {admin?.last_fetch_error ? (
            <p className="text-xs text-destructive mt-1">
              Last fetch failed: {admin.last_fetch_error}
              {admin.next_fetch_at ? ` · next try ${timeAgo(admin.next_fetch_at)}` : ''}
            </p>
          ) : null}
          {admin?.provider_state === 'error' || admin?.provider_error ? (
            <p className="text-xs text-destructive mt-1 flex items-center gap-1">
              <AlertTriangle className="w-3.5 h-3.5" />
              {admin.provider_error || 'Google Workspace reported an error'}
            </p>
          ) : null}
          {admin ? (
            <p className="text-xs text-muted-foreground mt-2">
              Roles in HR: {admin.roles.map((r) => r.group).join(', ')}
            </p>
          ) : null}
          {result ? <TestResult r={result} /> : null}
        </div>
        <div className="flex flex-wrap gap-2 shrink-0">
          {admin && m.connector === 'imap' ? (
            <Button size="sm" variant="secondary" onClick={() => test.mutate()} disabled={test.isPending}>
              Test connection
            </Button>
          ) : null}
          {admin?.provider_state === 'error' ? (
            <Button size="sm" variant="secondary" onClick={() => provision.mutate()} disabled={provision.isPending}>
              Create in Google again
            </Button>
          ) : null}
          {admin ? (
            <Button size="sm" variant="secondary" onClick={() => fetchNow.mutate()} disabled={fetchNow.isPending}>
              <RefreshCw className="w-3.5 h-3.5" />
              Fetch now
            </Button>
          ) : null}
          {m.access.work ? (
            <Button size="sm" variant="secondary" onClick={() => setOpen(open === 'settings' ? 'none' : 'settings')}>
              Signature &amp; replies
            </Button>
          ) : null}
          {admin ? (
            <>
              <Button size="sm" variant="secondary" onClick={() => setOpen(open === 'failures' ? 'none' : 'failures')}>
                Skipped mail
              </Button>
              <Button size="sm" variant="secondary" onClick={onEdit}>
                Edit
              </Button>
              <Button
                size="sm"
                variant="ghost"
                aria-label="Delete mailbox"
                onClick={() => {
                  if (confirm(`Move ${m.email} to the trash? Mail stops being fetched and sent${m.connector !== 'imap' ? ' and the Google mailbox is suspended' : ''}. Desk admins can restore it until it is purged.`))
                    remove.mutate();
                }}
              >
                <Trash2 className="w-4 h-4 text-destructive" />
              </Button>
            </>
          ) : null}
        </div>
      </div>
      {open === 'settings' ? <ManagerSettings m={m} /> : null}
      {open === 'failures' ? <ImportFailures id={m.id} /> : null}
    </Card>
  );
}

function MailboxTrash() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['mailboxes', 'trash'], queryFn: ({ signal }) => listTrashedMailboxes(signal) });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['mailboxes'] });
    void qc.invalidateQueries({ queryKey: ['me'] });
  };
  const restore = useMutation({ mutationFn: restoreMailbox, onSuccess: refresh });
  const purge = useMutation({ mutationFn: purgeMailbox, onSuccess: refresh });
  const rows = q.data?.mailboxes ?? [];
  if (rows.length === 0) return null;
  return (
    <section className="mt-8">
      <h2 className="font-heading font-bold mb-2">Trash</h2>
      <div className="space-y-2">
        {rows.map((m) => (
          <Card key={m.id} className="p-4 flex flex-wrap items-center justify-between gap-2">
            <div>
              <p className="font-semibold">{m.name}</p>
              <p className="text-xs text-muted-foreground">
                {m.email} · deleted {m.deleted_at ? timeAgo(m.deleted_at) : ''} · purged on {m.purge_after ? dateTime(m.purge_after) : '?'}
              </p>
            </div>
            <div className="flex gap-2">
              <Button size="sm" variant="secondary" onClick={() => restore.mutate(m.id)} disabled={restore.isPending}>
                Restore
              </Button>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => confirm(`Delete ${m.email} for good, with every conversation and file${m.connector !== 'imap' ? ' and the Google mailbox' : ''}? This cannot be undone.`) && purge.mutate(m.id)}
                disabled={purge.isPending}
              >
                Purge now
              </Button>
            </div>
          </Card>
        ))}
      </div>
      {purge.isError ? <p className="mt-2 text-sm text-destructive">{errorText(purge.error)}</p> : null}
    </section>
  );
}

/** Mailbox administration (Desk admins) and mailbox settings (managers). */
export function MailboxesPage() {
  const { isAdmin } = useMe();
  const q = useQuery({ queryKey: ['mailboxes'], queryFn: ({ signal }) => listMailboxes(signal) });
  const integrations = useQuery({ queryKey: ['integrations'], queryFn: ({ signal }) => getIntegrations(signal), enabled: isAdmin });
  const [editing, setEditing] = useState<MailboxAdmin | null | 'new'>(null);
  const boxes = (q.data?.mailboxes ?? []).filter((m) => isAdmin || m.access.work);
  return (
    <PageContainer className="max-w-4xl">
      <PageHeader
        title="Mailboxes"
        description="Each mailbox is a support address. Google Workspace mailboxes are created in Google; others are read over IMAP and answered over SMTP."
        actions={
          isAdmin && editing === null ? (
            <Button onClick={() => setEditing('new')}>
              <Plus className="w-4 h-4" />
              Add mailbox
            </Button>
          ) : undefined
        }
      />
      {editing !== null ? (
        <MailboxForm key={editing === 'new' ? 'new' : editing.id} editing={editing === 'new' ? null : editing} google={!!integrations.data?.google} onDone={() => setEditing(null)} />
      ) : null}
      {q.isLoading ? (
        <LoadingRow />
      ) : q.isError ? (
        <ErrorRow message="Could not load mailboxes." />
      ) : boxes.length === 0 && editing === null ? (
        <EmptyState icon={Mail} title="No mailboxes yet" description={isAdmin ? 'Add the first support address to start receiving conversations.' : 'You manage no mailbox.'} />
      ) : (
        <div className="space-y-3">
          {boxes.map((m) => (
            <MailboxRow key={m.id} m={m} onEdit={() => isAdminView(m) && setEditing(m)} />
          ))}
        </div>
      )}
      {isAdmin ? <MailboxTrash /> : null}
    </PageContainer>
  );
}
