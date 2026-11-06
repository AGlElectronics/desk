import { apiRequest } from '@/api/client';

export type MeMembership = {
  organization_id: string;
  role: string;
};

export type Me = {
  id: string;
  display_name: string;
  email: string;
  issuer: string;
  subject: string;
  memberships: MeMembership[];
  permissions: string[];
};

export function getMe(signal?: AbortSignal): Promise<Me> {
  return apiRequest<Me>('/api/v1/me', { signal });
}
