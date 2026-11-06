import { cn } from '@/lib/utils';

type Props = {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
};

export function PageHeader({ title, description, actions, className }: Props) {
  return (
    <div className={cn('flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between mb-6', className)}>
      <div>
        <h1 className="font-heading text-2xl font-extrabold leading-tight tracking-tight">{title}</h1>
        {description ? <p className="text-sm text-muted-foreground mt-1">{description}</p> : null}
      </div>
      {actions ? <div className="flex flex-wrap gap-2">{actions}</div> : null}
    </div>
  );
}

export function PageContainer({ className, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={cn(
        'w-full max-w-[min(100%,88rem)] mx-auto px-4 sm:px-6 lg:px-8 py-4 sm:py-6 flex-1',
        className
      )}
      {...rest}
    />
  );
}
