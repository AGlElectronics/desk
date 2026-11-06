import type { Status } from '@/api/desk';
import { cn } from '@/lib/utils';

const STYLES: Record<Status, string> = {
  active: 'bg-primary-soft text-primary',
  pending: 'bg-warning/15 text-warning',
  closed: 'bg-secondary text-muted-foreground',
  spam: 'bg-destructive/10 text-destructive',
};

export const STATUS_LABELS: Record<Status, string> = {
  active: 'Active',
  pending: 'Pending',
  closed: 'Closed',
  spam: 'Spam',
};

export function StatusBadge({ status, className }: { status: Status; className?: string }) {
  return (
    <span className={cn('text-[11px] font-semibold rounded-pill px-2 py-0.5', STYLES[status], className)}>
      {STATUS_LABELS[status]}
    </span>
  );
}
