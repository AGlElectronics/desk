import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useParams } from '@tanstack/react-router';
import { GitMerge, X } from 'lucide-react';
import { getCustomer, mergeCustomer, searchCustomers, updateCustomer } from '@/api/desk';
import { ApiError } from '@/api/client';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { ConversationRow } from '@/pages/InboxPage';
import { timeAgo } from '@/lib/format';

const errorText = (e: unknown) => (e instanceof ApiError ? Object.values(e.fields())[0] ?? e.message : (e as Error).message);

/** A customer's addresses, notes and every conversation you can see. */
export function CustomerPage() {
  const { customerId } = useParams({ strict: false }) as { customerId: string };
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ['customer', customerId], queryFn: ({ signal }) => getCustomer(customerId, signal) });
  const [name, setName] = useState('');
  const [notes, setNotes] = useState('');
  const [newEmail, setNewEmail] = useState('');
  const [find, setFind] = useState('');
  useEffect(() => {
    if (q.data) {
      setName(q.data.name);
      setNotes(q.data.notes);
    }
  }, [q.data]);
  const done = (d: unknown) => {
    qc.setQueryData(['customer', customerId], d);
    void qc.invalidateQueries({ queryKey: ['conversation'] });
  };
  const save = useMutation({ mutationFn: (body: Parameters<typeof updateCustomer>[1]) => updateCustomer(customerId, body), onSuccess: done });
  const merge = useMutation({ mutationFn: (source: string) => mergeCustomer(customerId, source), onSuccess: done });
  const others = useQuery({
    queryKey: ['customers', find],
    queryFn: ({ signal }) => searchCustomers(find, signal),
    enabled: find.trim().length >= 2,
  });

  if (q.isLoading) return <LoadingRow />;
  if (q.isError || !q.data) return <ErrorRow message="Could not load this customer." />;
  const c = q.data;
  return (
    <PageContainer className="max-w-5xl">
      <PageHeader title={c.name || c.email} description={`Customer since ${timeAgo(c.created_at)}`} />
      <div className="grid gap-6 lg:grid-cols-[1fr_22rem]">
        <section>
          <h2 className="font-heading font-bold mb-2">Conversations</h2>
          {c.conversations.length === 0 ? (
            <p className="text-sm text-muted-foreground">None in your mailboxes.</p>
          ) : (
            <Card className="divide-y divide-border overflow-hidden">
              {c.conversations.map((conv) => (
                <ConversationRow key={conv.id} c={conv} />
              ))}
            </Card>
          )}
        </section>
        <aside className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Details</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              <div>
                <Label htmlFor="cu-name">Name</Label>
                <Input id="cu-name" value={name} readOnly={!c.can_edit} onChange={(e) => setName(e.target.value)} onBlur={() => c.can_edit && name !== c.name && save.mutate({ name })} />
              </div>
              <div>
                <Label htmlFor="cu-notes">Notes</Label>
                <textarea
                  id="cu-notes"
                  rows={4}
                  readOnly={!c.can_edit}
                  className="w-full rounded-md border border-input bg-background p-2 text-sm"
                  value={notes}
                  onChange={(e) => setNotes(e.target.value)}
                  onBlur={() => c.can_edit && notes !== c.notes && save.mutate({ notes })}
                />
              </div>
              <div>
                <p className="text-sm font-semibold mb-1">Addresses</p>
                <ul className="space-y-1 text-sm">
                  {c.emails.map((e) => (
                    <li key={e} className="flex items-center justify-between">
                      <span>
                        {e}
                        {e === c.email ? <span className="text-xs text-muted-foreground"> (main)</span> : null}
                      </span>
                      {c.can_edit && e !== c.email ? (
                        <button type="button" aria-label={`Remove ${e}`} onClick={() => save.mutate({ remove_email: e })}>
                          <X className="w-3.5 h-3.5" />
                        </button>
                      ) : null}
                    </li>
                  ))}
                </ul>
                {c.can_edit ? (
                  <form
                    className="mt-2 flex gap-2"
                    onSubmit={(e) => {
                      e.preventDefault();
                      save.mutate({ add_email: newEmail }, { onSuccess: () => setNewEmail('') });
                    }}
                  >
                    <Input aria-label="Add address" type="email" placeholder="another@address" value={newEmail} onChange={(e) => setNewEmail(e.target.value)} />
                    <Button size="sm" type="submit" disabled={!newEmail}>
                      Add
                    </Button>
                  </form>
                ) : null}
              </div>
              {save.isError ? <p className="text-xs text-destructive">{errorText(save.error)}</p> : null}
            </CardContent>
          </Card>
          {c.can_edit ? (
            <Card>
              <CardHeader>
                <CardTitle>Merge another customer into this one</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                <Input aria-label="Find customer" placeholder="Name or address" value={find} onChange={(e) => setFind(e.target.value)} />
                <ul className="space-y-1 text-sm">
                  {(others.data?.customers ?? [])
                    .filter((o) => o.id !== c.id)
                    .map((o) => (
                      <li key={o.id} className="flex items-center justify-between gap-2">
                        <span className="truncate">
                          {o.name ? `${o.name} · ` : ''}
                          {o.email}
                        </span>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => confirm(`Move every address and conversation of ${o.email} to this customer? ${o.email} is removed as a separate customer.`) && merge.mutate(o.id)}
                        >
                          <GitMerge className="w-3.5 h-3.5" />
                          Merge
                        </Button>
                      </li>
                    ))}
                </ul>
                {merge.isError ? <p className="text-xs text-destructive">{errorText(merge.error)}</p> : null}
              </CardContent>
            </Card>
          ) : null}
          <Link to="/inbox" search={{ folder: 'unassigned' }} className="text-sm text-muted-foreground hover:underline">
            Back to the inbox
          </Link>
        </aside>
      </div>
    </PageContainer>
  );
}
