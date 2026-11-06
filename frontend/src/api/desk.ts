import { apiRequest } from '@/api/client';

export type Security = 'tls' | 'starttls' | 'none';

export type FolderCounts = {
  unassigned: number;
  mine: number;
  assigned: number;
  pending: number;
  closed: number;
  spam: number;
};

export type Mailbox = {
  id: string;
  name: string;
  email: string;
  signature: string;
  imap_host: string;
  imap_port: number;
  imap_security: Security;
  imap_username: string;
  imap_password_set: boolean;
  smtp_host: string;
  smtp_port: number;
  smtp_security: Security;
  smtp_username: string;
  smtp_password_set: boolean;
  last_fetch_at?: string;
  last_fetch_error: string;
  created_at: string;
};

export type MailboxWithCounts = Mailbox & { counts: FolderCounts };

export type MailboxInput = Partial<{
  name: string;
  email: string;
  signature: string;
  imap_host: string;
  imap_port: number;
  imap_security: Security;
  imap_username: string;
  imap_password: string;
  smtp_host: string;
  smtp_port: number;
  smtp_security: Security;
  smtp_username: string;
  smtp_password: string;
}>;

export type Status = 'active' | 'pending' | 'closed' | 'spam';
export type Folder = 'unassigned' | 'mine' | 'assigned' | 'pending' | 'closed' | 'spam' | 'all';

export type Customer = { id: string; email: string; name: string; notes: string };

export type Conversation = {
  id: string;
  mailbox_id: string;
  mailbox_name: string;
  number: number;
  subject: string;
  status: Status;
  preview: string;
  customer: Customer;
  assignee_id?: string;
  assignee_name?: string;
  thread_count: number;
  last_activity_at: string;
  created_at: string;
  closed_at?: string;
};

export type Attachment = { id: string; filename: string; content_type: string; size_bytes: number };

export type Thread = {
  id: string;
  kind: 'customer' | 'reply' | 'note' | 'system';
  author_id?: string;
  author_name?: string;
  from_email: string;
  to: string[];
  cc: string[];
  body_html: string;
  body_text: string;
  send_status?: 'queued' | 'sent' | 'failed';
  send_error?: string;
  next_attempt_at?: string;
  attachments: Attachment[];
  created_at: string;
};

export type Viewer = { user_id: string; display_name: string; mode: 'viewing' | 'replying' };

export type ConversationDetail = Conversation & {
  threads: Thread[];
  history: Conversation[];
  viewers: Viewer[];
};

export type Person = { id: string; display_name: string; email: string };

export type ConnectionTest = {
  imap_ok: boolean;
  imap_error?: string;
  smtp_ok: boolean;
  smtp_error?: string;
  smtp_via: string;
};

export function listMailboxes(signal?: AbortSignal) {
  return apiRequest<{ mailboxes: MailboxWithCounts[] }>('/api/v1/mailboxes', { signal });
}

export function createMailbox(body: MailboxInput) {
  return apiRequest<Mailbox>('/api/v1/mailboxes', { method: 'POST', body });
}

export function updateMailbox(id: string, body: MailboxInput) {
  return apiRequest<Mailbox>(`/api/v1/mailboxes/${id}`, { method: 'PATCH', body });
}

export function deleteMailbox(id: string) {
  return apiRequest<void>(`/api/v1/mailboxes/${id}`, { method: 'DELETE' });
}

export function testMailbox(id: string) {
  return apiRequest<ConnectionTest>(`/api/v1/mailboxes/${id}/test`, { method: 'POST' });
}

export function fetchMailbox(id: string) {
  return apiRequest<{ queued: boolean }>(`/api/v1/mailboxes/${id}/fetch`, { method: 'POST' });
}

export function listConversations(
  params: { mailbox?: string; folder: Folder; q?: string },
  signal?: AbortSignal
) {
  return apiRequest<{ conversations: Conversation[] }>('/api/v1/conversations', { query: params, signal });
}

export function createConversation(body: {
  mailbox_id: string;
  customer_email: string;
  customer_name?: string;
  subject: string;
  body: string;
}) {
  return apiRequest<Conversation>('/api/v1/conversations', { method: 'POST', body });
}

export function getConversation(id: string, signal?: AbortSignal) {
  return apiRequest<ConversationDetail>(`/api/v1/conversations/${id}`, { signal });
}

export function updateConversation(id: string, body: { status?: Status; assignee_id?: string }) {
  return apiRequest<Conversation>(`/api/v1/conversations/${id}`, { method: 'PATCH', body });
}

export function addThread(id: string, body: { kind: 'reply' | 'note'; body: string; status?: Status }) {
  return apiRequest<{ id: string }>(`/api/v1/conversations/${id}/threads`, { method: 'POST', body });
}

export function sendHeartbeat(id: string, mode: 'viewing' | 'replying') {
  return apiRequest<void>(`/api/v1/conversations/${id}/viewing`, { method: 'POST', body: { mode } });
}

export function undoReply(threadId: string) {
  return apiRequest<void>(`/api/v1/threads/${threadId}/undo`, { method: 'POST' });
}

export function retryReply(threadId: string) {
  return apiRequest<void>(`/api/v1/threads/${threadId}/retry`, { method: 'POST' });
}

export function updateCustomer(id: string, body: { name?: string; notes?: string }) {
  return apiRequest<void>(`/api/v1/customers/${id}`, { method: 'PATCH', body });
}

export function listPeople(signal?: AbortSignal) {
  return apiRequest<{ people: Person[] }>('/api/v1/users', { signal });
}
