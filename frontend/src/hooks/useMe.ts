import { useQuery } from '@tanstack/react-query';
import { getMe, type Me } from '@/api/me';
import type { MailboxRights } from '@/api/desk';

/** Keep the permission names in sync with internal/domain/domain.go. */
export const PERMS = {
  mailboxManage: 'mailbox.manage',
  settingsManage: 'settings.manage',
} as const;

const NO_RIGHTS: MailboxRights = { view: false, work: false, delete: false, admin: false };

export type MeHook = {
  me: Me | undefined;
  isLoading: boolean;
  isSuccess: boolean;
  can: (perm: string) => boolean;
  canAny: (...perms: string[]) => boolean;
  /** Desk admin somewhere. */
  isAdmin: boolean;
  /** Rights in one mailbox (all false when unknown). */
  rights: (mailboxId: string | undefined) => MailboxRights;
  /** The user may work (manage) at least one mailbox. */
  worksAny: boolean;
};

/**
 * useMe loads /api/v1/me and exposes small helpers for navigation gating.
 * Precise authorization enforcement happens server-side; the SPA uses this
 * only to hide buttons the user can never use.
 */
export function useMe(): MeHook {
  const q = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => getMe(signal),
    retry: false,
    staleTime: 30_000,
    refetchOnWindowFocus: true,
  });
  const perms = q.data?.permissions ?? [];
  const boxes = q.data?.mailboxes ?? [];
  return {
    me: q.data,
    isLoading: q.isLoading,
    isSuccess: q.isSuccess,
    can: (perm) => perms.includes(perm),
    canAny: (...needed) => needed.some((p) => perms.includes(p)),
    isAdmin: q.data?.is_admin ?? false,
    rights: (id) => boxes.find((m) => m.id === id)?.access ?? NO_RIGHTS,
    worksAny: boxes.some((m) => m.access.work),
  };
}
