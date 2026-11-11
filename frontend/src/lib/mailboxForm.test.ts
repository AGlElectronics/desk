import { describe, expect, it } from 'vitest';
import type { MailboxAdmin } from '@/api/desk';
import { EMPTY, toForm, toInput } from './mailboxForm';

const form = {
  ...EMPTY,
  slug: ' info ',
  name: 'Info',
  email: 'info@con.example',
  imap_host: 'imap.example.org',
  imap_username: 'info',
  imap_password: 'change-me',
  smtp_host: 'smtp.example.org',
  smtp_port: 'abc',
  smtp_username: 'info',
  smtp_password: 'change-me',
};

describe('toInput', () => {
  it('sends connector, trimmed slug and both passwords on create', () => {
    const input = toInput(form, null);
    expect(input).toMatchObject({ connector: 'imap', slug: 'info', imap_port: 993, smtp_port: 587 });
    expect(input.imap_password).toBe('change-me');
    expect(input.smtp_password).toBe('change-me');
  });

  it('keeps stored passwords when the fields stay blank on edit', () => {
    const editing = { id: 'm1' } as MailboxAdmin;
    const input = toInput({ ...form, imap_password: '', smtp_password: 'new-secret' }, editing);
    expect(input).not.toHaveProperty('connector');
    expect(input).not.toHaveProperty('slug');
    expect(input).not.toHaveProperty('imap_password');
    expect(input.smtp_password).toBe('new-secret');
  });

  it('sends no server settings for Google mailboxes', () => {
    const input = toInput({ ...form, connector: 'google_group' }, null);
    expect(input.connector).toBe('google_group');
    expect(input).not.toHaveProperty('imap_host');
    expect(input).not.toHaveProperty('smtp_password');
  });
});

describe('toForm', () => {
  it('never pre-fills passwords', () => {
    const m = {
      connector: 'imap',
      slug: 'info',
      name: 'Info',
      email: 'info@con.example',
      signature: '',
      imap_host: 'imap.example.org',
      imap_port: 993,
      imap_security: 'tls',
      imap_username: 'info',
      imap_move_to: 'Desk',
      smtp_host: 'smtp.example.org',
      smtp_port: 465,
      smtp_security: 'tls',
      smtp_username: 'info',
    } as MailboxAdmin;
    const f = toForm(m);
    expect(f).toMatchObject({ imap_port: '993', smtp_port: '465', imap_move_to: 'Desk', imap_password: '', smtp_password: '' });
  });
});
