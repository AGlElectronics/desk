import { apiRequest } from '@/api/client';
import type { MailboxRights } from '@/api/desk';

export type MeMembership = {
  organization_id: string;
  role: string;
};

export type MeMailbox = { id: string; slug: string; name: string; email: string; access: MailboxRights };

export type Me = {
  id: string;
  display_name: string;
  email: string;
  issuer: string;
  subject: string;
  memberships: MeMembership[];
  permissions: string[];
  /** Desk admin (mailbox.manage) in at least one organization. */
  is_admin: boolean;
  /** Mailboxes the user can see, with their rights. */
  mailboxes: MeMailbox[];
};

export function getMe(signal?: AbortSignal): Promise<Me> {
  return apiRequest<Me>('/api/v1/me', { signal });
}
