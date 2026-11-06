import { forwardRef } from 'react';
import { cn } from '@/lib/utils';

const fieldClasses =
  'w-full rounded-md border border-input bg-background text-sm placeholder:text-muted-foreground transition-colors focus-visible:outline-none focus-visible:border-primary focus-visible:ring-[3px] focus-visible:ring-primary-soft disabled:cursor-not-allowed disabled:opacity-50';

export const Input = forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  function Input({ className, ...rest }, ref) {
    return <input ref={ref} className={cn('flex h-9 px-3 py-1', fieldClasses, className)} {...rest} />;
  }
);

export function Label({
  htmlFor,
  children,
  className,
}: {
  htmlFor?: string;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <label htmlFor={htmlFor} className={cn('text-sm font-semibold block mb-1', className)}>
      {children}
    </label>
  );
}
