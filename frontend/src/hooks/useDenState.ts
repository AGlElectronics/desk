import { useQuery } from '@tanstack/react-query';
import { ApiError, apiRequest } from '@/api/client';

export type DenState = { mode: 'required' | 'standalone'; paired: boolean };

/** Backends from before DEN_MODE have no state endpoint (404); they never require pairing. */
export async function getDenState(signal?: AbortSignal): Promise<DenState> {
  try {
    return await apiRequest<DenState>('/api/den/state', { signal });
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return { mode: 'standalone', paired: false };
    throw err;
  }
}

/** Whether the backend runs with DEN_MODE=required and is not paired yet. */
export function isDenPairingRequired(state: DenState | undefined): boolean {
  return state?.mode === 'required' && !state.paired;
}

export function useDenState() {
  return useQuery({
    queryKey: ['den-state'],
    queryFn: ({ signal }) => getDenState(signal),
    retry: false,
    staleTime: 30_000,
    refetchOnWindowFocus: true,
  });
}