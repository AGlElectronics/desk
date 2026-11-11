import { apiRequest } from '@/api/client';

export type Security = 'tls' | 'starttls' | 'none';
export type Connector = 'imap' | 'google_user' | 'google_group';

export type FolderCounts = {
  unassigned: number;
  mine: number;
  assigned: number;
  pending: number;
  closed: number;
  spam: number;
  starred: number;
  trash: number;
};

export type MailboxRights = { view: boolean; work: boolean; delete: boolean; admin: boolean };

export type AutoReply = { enabled: boolean; subject: string; body_html: string; body_text: string };

/** What everyone with access sees of a mailbox. */
export type MailboxSummary = {
  id: string;
  organization_id: string;
  slug: string;
  name: string;
  email: string;
  connector: Connector;
  signature: string;
  auto_reply: AutoReply;
  access: MailboxRights;
  counts: FolderCounts;
};

export type MailboxRole = { key: string; group: string; level: 'viewer' | 'manager' | 'delete' };

/** Desk admins additionally get the configuration. */
export type MailboxAdmin = MailboxSummary & {
  imap_host: string;
  imap_port: number;
  imap_security: Security;
  imap_username: string;
  imap_password_set: boolean;
  imap_move_to: string;
  smtp_host: string;
  smtp_port: number;
  smtp_security: Security;
  smtp_username: string;
  smtp_password_set: boolean;
  last_fetch_at?: string;
  last_fetch_error: string;
  fetch_failures: number;
  next_fetch_at?: string;
  provider_state: '' | 'provisioning' | 'active' | 'suspended' | 'error';
  provider_error: string;
  deleted_at?: string;
  purge_after?: string;
  created_at: string;
  roles: MailboxRole[];
};

export type Mailbox = MailboxSummary | MailboxAdmin;

export const isAdminView = (m: Mailbox): m is MailboxAdmin => 'imap_host' in m;

export type MailboxInput = Partial<{
  connector: Connector;
  slug: string;
  organization_id: string;
  name: string;
  email: string;
  signature: string;
  auto_reply: AutoReply;
  imap_host: string;
  imap_port: number;
  imap_security: Security;
  imap_username: string;
  imap_password: string;
  imap_move_to: string;
  smtp_host: string;
  smtp_port: number;
  smtp_security: Security;
  smtp_username: string;
  smtp_password: string;
}>;

export type Status = 'active' | 'pending' | 'closed' | 'spam';
export type Folder = 'unassigned' | 'mine' | 'assigned' | 'pending' | 'closed' | 'spam' | 'all' | 'starred' | 'trash';

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
  tags: string[];
  starred: boolean;
  last_activity_at: string;
  created_at: string;
  closed_at?: string;
  deleted_at?: string;
};

export type Attachment = {
  id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  content_id?: string;
  inline: boolean;
};

export type ThreadKind = 'customer' | 'reply' | 'note' | 'system' | 'forward' | 'auto_reply';

export type Thread = {
  id: string;
  kind: ThreadKind;
  author_id?: string;
  author_name?: string;
  from_email: string;
  to: string[];
  cc: string[];
  body_html: string;
  body_text: string;
  send_status?: 'queued' | 'sending' | 'sent' | 'failed';
  send_error?: string;
  next_attempt_at?: string;
  auto_submitted: boolean;
  deleted_at?: string;
  attachments: Attachment[];
  created_at: string;
};

export type Viewer = { user_id: string; display_name: string; mode: 'viewing' | 'replying' };

export type ConversationDetail = Conversation & {
  threads: Thread[];
  threads_truncated: boolean;
  history: Conversation[];
  viewers: Viewer[];
  access: MailboxRights;
};

export type ConversationPage = { conversations: Conversation[]; next_cursor: string };

export type Person = { id: string; display_name: string; email: string };

export type ConnectionTest = {
  imap_ok: boolean;
  imap_error?: string;
  smtp_ok: boolean;
  smtp_error?: string;
  smtp_via: string;
};

export type TrashResult = { warning?: string };

export type SavedReply = { id: string; mailbox_id: string | null; name: string; body_html: string; body_text: string; updated_at: string };

export type CustomerProfile = Customer & {
  organization_id: string;
  emails: string[];
  conversations: Conversation[];
  created_at: string;
  can_edit: boolean;
};

export type ImportFailure = { id: string; source: string; subject: string; from_email: string; size_bytes: number; error: string; created_at: string };

// ---------------------------------------------------------------------------
// Mailboxes
// ---------------------------------------------------------------------------

export const listMailboxes = (signal?: AbortSignal) => apiRequest<{ mailboxes: Mailbox[] }>('/api/v1/mailboxes', { signal });
export const getMailbox = (id: string, signal?: AbortSignal) => apiRequest<Mailbox>(`/api/v1/mailboxes/${id}`, { signal });
export const createMailbox = (body: MailboxInput) => apiRequest<MailboxAdmin>('/api/v1/mailboxes', { method: 'POST', body });
export const updateMailbox = (id: string, body: MailboxInput) => apiRequest<Mailbox>(`/api/v1/mailboxes/${id}`, { method: 'PATCH', body });
export const deleteMailbox = (id: string) => apiRequest<MailboxAdmin>(`/api/v1/mailboxes/${id}`, { method: 'DELETE' });
export const restoreMailbox = (id: string) => apiRequest<MailboxAdmin>(`/api/v1/mailboxes/${id}/restore`, { method: 'POST' });
export const purgeMailbox = (id: string) => apiRequest<void>(`/api/v1/mailboxes/${id}/purge`, { method: 'POST' });
export const provisionMailbox = (id: string) => apiRequest<MailboxAdmin>(`/api/v1/mailboxes/${id}/provision`, { method: 'POST' });
export const listTrashedMailboxes = (signal?: AbortSignal) => apiRequest<{ mailboxes: MailboxAdmin[] }>('/api/v1/mailboxes/trash', { signal });
export const testMailbox = (id: string) => apiRequest<ConnectionTest>(`/api/v1/mailboxes/${id}/test`, { method: 'POST' });
export const fetchMailbox = (id: string) => apiRequest<{ queued: boolean }>(`/api/v1/mailboxes/${id}/fetch`, { method: 'POST' });
export const listImportFailures = (id: string, signal?: AbortSignal) =>
  apiRequest<{ failures: ImportFailure[] }>(`/api/v1/mailboxes/${id}/import-failures`, { signal });

// ---------------------------------------------------------------------------
// Conversations
// ---------------------------------------------------------------------------

export type ListParams = { mailbox?: string; folder: Folder; q?: string; tag?: string; cursor?: string; limit?: number };

export const listConversations = (params: ListParams, signal?: AbortSignal) =>
  apiRequest<ConversationPage>('/api/v1/conversations', { query: params, signal });

export const createConversation = (body: {
  mailbox_id: string;
  customer_email: string;
  customer_name?: string;
  subject: string;
  body_html: string;
  cc?: string[];
}) => apiRequest<Conversation>('/api/v1/conversations', { method: 'POST', body });

export const getConversation = (id: string, signal?: AbortSignal) => apiRequest<ConversationDetail>(`/api/v1/conversations/${id}`, { signal });

export const updateConversation = (id: string, body: { status?: Status; assignee_id?: string; tags?: string[] }) =>
  apiRequest<Conversation>(`/api/v1/conversations/${id}`, { method: 'PATCH', body });

export const deleteConversation = (id: string) => apiRequest<TrashResult>(`/api/v1/conversations/${id}`, { method: 'DELETE' });
export const restoreConversation = (id: string) => apiRequest<TrashResult>(`/api/v1/conversations/${id}/restore`, { method: 'POST' });

export type ThreadBody = { kind: 'reply' | 'note'; body_html: string; cc?: string[]; status?: Status };
export const addThread = (id: string, body: ThreadBody) =>
  apiRequest<{ id: string }>(`/api/v1/conversations/${id}/threads`, { method: 'POST', body });

export const forwardConversation = (id: string, body: { to: string[]; body_html?: string; thread_id?: string }) =>
  apiRequest<{ id: string }>(`/api/v1/conversations/${id}/forward`, { method: 'POST', body });

export const mergeConversation = (id: string, intoId: string) =>
  apiRequest<Conversation>(`/api/v1/conversations/${id}/merge`, { method: 'POST', body: { into_id: intoId } });

export const moveConversation = (id: string, mailboxId: string) =>
  apiRequest<Conversation>(`/api/v1/conversations/${id}/move`, { method: 'POST', body: { mailbox_id: mailboxId } });

export const setStar = (id: string, on: boolean) => apiRequest<void>(`/api/v1/conversations/${id}/star`, { method: on ? 'PUT' : 'DELETE' });

export const sendHeartbeat = (id: string, mode: 'viewing' | 'replying') =>
  apiRequest<void>(`/api/v1/conversations/${id}/viewing`, { method: 'POST', body: { mode } });

export const undoReply = (threadId: string) => apiRequest<void>(`/api/v1/threads/${threadId}/undo`, { method: 'POST' });
export const retryReply = (threadId: string) => apiRequest<void>(`/api/v1/threads/${threadId}/retry`, { method: 'POST' });
export const deleteThread = (threadId: string) => apiRequest<TrashResult>(`/api/v1/threads/${threadId}`, { method: 'DELETE' });
export const restoreThread = (threadId: string) => apiRequest<TrashResult>(`/api/v1/threads/${threadId}/restore`, { method: 'POST' });

// ---------------------------------------------------------------------------
// Customers, people, tags, saved replies
// ---------------------------------------------------------------------------

export const searchCustomers = (q: string, signal?: AbortSignal) => apiRequest<{ customers: Customer[] }>('/api/v1/customers', { query: { q }, signal });
export const getCustomer = (id: string, signal?: AbortSignal) => apiRequest<CustomerProfile>(`/api/v1/customers/${id}`, { signal });
export const updateCustomer = (id: string, body: { name?: string; notes?: string; add_email?: string; remove_email?: string }) =>
  apiRequest<CustomerProfile>(`/api/v1/customers/${id}`, { method: 'PATCH', body });
export const mergeCustomer = (id: string, sourceId: string) =>
  apiRequest<CustomerProfile>(`/api/v1/customers/${id}/merge`, { method: 'POST', body: { source_id: sourceId } });

export const listPeople = (mailbox: string, signal?: AbortSignal) => apiRequest<{ people: Person[] }>('/api/v1/users', { query: { mailbox }, signal });
export const listTags = (mailbox?: string, signal?: AbortSignal) => apiRequest<{ tags: string[] }>('/api/v1/tags', { query: { mailbox }, signal });

export const listSavedReplies = (mailbox: string, signal?: AbortSignal) =>
  apiRequest<{ saved_replies: SavedReply[] }>(`/api/v1/mailboxes/${mailbox}/saved-replies`, { signal });
export const createSavedReply = (body: { mailbox_id: string | null; name: string; body_html: string }) =>
  apiRequest<SavedReply>('/api/v1/saved-replies', { method: 'POST', body });
export const updateSavedReply = (id: string, body: { name: string; body_html: string }) =>
  apiRequest<SavedReply>(`/api/v1/saved-replies/${id}`, { method: 'PATCH', body });
export const deleteSavedReply = (id: string) => apiRequest<void>(`/api/v1/saved-replies/${id}`, { method: 'DELETE' });

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

export type Retention = {
  closed_conversation_days: number | null;
  attachment_days: number | null;
  raw_mail_days: number | null;
  trash_days: number | null;
  mailbox_trash_days: number | null;
};

export type RetentionView = {
  effective: Retention;
  desk: Retention;
  den?: Partial<Retention>;
  managed_by: Partial<Record<keyof Retention, 'den'>>;
  pending_legal_review: (keyof Retention)[];
};

export type Integrations = {
  google: null | {
    domain: string;
    shared_mailbox: string;
    label: string;
    shared_status: { last_fetch_at?: string; error: string; unrouted: number };
  };
  notifications: boolean;
  notify_from: string;
  module_smtp: boolean;
};

export type AuditEntry = {
  id: number;
  actor_name: string;
  action: string;
  mailbox_id?: string;
  target_type: string;
  target_id: string;
  summary: string;
  created_at: string;
};

export type NotificationPrefs = { new_conversation: boolean; assigned_to_me: boolean; available: boolean };

export const getRetention = (signal?: AbortSignal) => apiRequest<RetentionView>('/api/v1/settings/retention', { signal });
export const putRetention = (body: Partial<Retention>) => apiRequest<RetentionView>('/api/v1/settings/retention', { method: 'PUT', body });
export const getIntegrations = (signal?: AbortSignal) => apiRequest<Integrations>('/api/v1/settings/integrations', { signal });
export const getAudit = (params: { before?: number; mailbox?: string }, signal?: AbortSignal) =>
  apiRequest<{ entries: AuditEntry[] }>('/api/v1/audit', { query: params, signal });
export const getNotifications = (signal?: AbortSignal) => apiRequest<NotificationPrefs>('/api/v1/me/notifications', { signal });
export const putNotifications = (body: { new_conversation: boolean; assigned_to_me: boolean }) =>
  apiRequest<NotificationPrefs>('/api/v1/me/notifications', { method: 'PUT', body });
