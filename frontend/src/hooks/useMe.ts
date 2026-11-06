import { useQuery } from '@tanstack/react-query';
import { getMe, type Me } from '@/api/me';

/** Keep the permission names in sync with internal/domain/domain.go. */
export const PERMS = {
  ticketsWork: 'tickets.work',
  mailboxManage: 'mailbox.manage',
  settingsManage: 'settings.manage',
} as const;

export type MeHook = {
  me: Me | undefined;
  isLoading: boolean;
  isSuccess: boolean;
  can: (perm: string) => boolean;
  canAny: (...perms: string[]) => boolean;
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
  const can = (perm: string) => perms.includes(perm);
  const canAny = (...needed: string[]) => needed.some((p) => perms.includes(p));
  return {
    me: q.data,
    isLoading: q.isLoading,
    isSuccess: q.isSuccess,
    can,
    canAny,
  };
}
