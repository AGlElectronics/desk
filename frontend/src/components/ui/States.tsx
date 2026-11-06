import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';

export function LoadingRow({ message = 'Loading…' }: { message?: string }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground py-6 justify-center">
      <Loader2 className="w-4 h-4 animate-spin" />
      <span>{message}</span>
    </div>
  );
}

export function ErrorRow({ message }: { message: string }) {
  return <div className="text-sm text-destructive py-6 text-center">{message}</div>;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: {
  icon?: React.ComponentType<{ className?: string }>;
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn('flex flex-col items-center justify-center text-center py-12 px-4', className)}>
      {Icon ? (
        <div className="mb-3 rounded-pill bg-primary-soft p-3">
          <Icon className="w-6 h-6 text-primary" />
        </div>
      ) : null}
      <h3 className="font-heading font-bold">{title}</h3>
      {description ? <p className="text-sm text-muted-foreground mt-1 max-w-sm">{description}</p> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
