import type { Connector, MailboxAdmin, MailboxInput, Security } from '@/api/desk';

// The mailbox form's state and how it becomes an API request.

export type Form = {
  connector: Connector;
  slug: string;
  name: string;
  email: string;
  signature: string;
  imap_host: string;
  imap_port: string;
  imap_security: Security;
  imap_username: string;
  imap_password: string;
  imap_move_to: string;
  smtp_host: string;
  smtp_port: string;
  smtp_security: Security;
  smtp_username: string;
  smtp_password: string;
};

export const EMPTY: Form = {
  connector: 'imap',
  slug: '',
  name: '',
  email: '',
  signature: '--\n{agent.name}',
  imap_host: '',
  imap_port: '993',
  imap_security: 'tls',
  imap_username: '',
  imap_password: '',
  imap_move_to: '',
  smtp_host: '',
  smtp_port: '587',
  smtp_security: 'starttls',
  smtp_username: '',
  smtp_password: '',
};

export const CONNECTOR_LABELS: Record<Connector, string> = {
  imap: 'IMAP / SMTP server',
  google_user: 'Google Workspace: own licensed user',
  google_group: 'Google Workspace: group into the shared account',
};

export function toForm(m: MailboxAdmin): Form {
  return {
    ...EMPTY,
    connector: m.connector,
    slug: m.slug,
    name: m.name,
    email: m.email,
    signature: m.signature,
    imap_host: m.imap_host,
    imap_port: String(m.imap_port),
    imap_security: m.imap_security,
    imap_username: m.imap_username,
    imap_move_to: m.imap_move_to,
    smtp_host: m.smtp_host,
    smtp_port: String(m.smtp_port),
    smtp_security: m.smtp_security,
    smtp_username: m.smtp_username,
  };
}

/** The request body for a create or an update. */
export function toInput(f: Form, editing: MailboxAdmin | null): MailboxInput {
  const input: MailboxInput = { name: f.name, email: f.email, signature: f.signature };
  if (!editing) {
    input.connector = f.connector;
    if (f.slug.trim()) input.slug = f.slug.trim();
  }
  if (f.connector === 'imap') {
    Object.assign(input, {
      imap_host: f.imap_host,
      imap_port: Number(f.imap_port) || 993,
      imap_security: f.imap_security,
      imap_username: f.imap_username,
      imap_move_to: f.imap_move_to,
      smtp_host: f.smtp_host,
      smtp_port: Number(f.smtp_port) || 587,
      smtp_security: f.smtp_security,
      smtp_username: f.smtp_username,
    });
    // Blank password on edit keeps the stored one.
    if (f.imap_password || !editing) input.imap_password = f.imap_password;
    if (f.smtp_password || !editing) input.smtp_password = f.smtp_password;
  }
  return input;
}
