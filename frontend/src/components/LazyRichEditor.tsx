import { forwardRef, lazy, Suspense } from 'react';
import type { RichEditorHandle } from '@/components/RichEditor';

const RichEditor = lazy(() => import('@/components/RichEditor'));

type Props = {
  initialHTML?: string;
  placeholder?: string;
  onChange?: (empty: boolean) => void;
  ariaLabel?: string;
};

/** The rich text editor, loaded on first use to keep the inbox light. */
export const LazyRichEditor = forwardRef<RichEditorHandle, Props>(function LazyRichEditor(props, ref) {
  return (
    <Suspense fallback={<div className="rounded-md border border-input bg-background min-h-[8rem] p-3 text-sm text-muted-foreground">Loading editor…</div>}>
      <RichEditor ref={ref} {...props} />
    </Suspense>
  );
});

export type { RichEditorHandle };
