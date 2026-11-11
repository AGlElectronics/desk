import { useState } from 'react';
import { X } from 'lucide-react';
import { SHORTCUT_HELP, useShortcuts } from '@/hooks/useShortcuts';
import { Button } from '@/components/ui/Button';
import { Card } from '@/components/ui/Card';

/** "?" opens the list of keyboard shortcuts. Mounted once in the layout. */
export function ShortcutHelp() {
  const [open, setOpen] = useState(false);
  useShortcuts({ '?': () => setOpen(true), Escape: () => setOpen(false) });
  if (!open) return null;
  return (
    <div role="dialog" aria-label="Keyboard shortcuts" className="fixed inset-0 z-50 grid place-items-center bg-black/40 p-4" onClick={() => setOpen(false)}>
      <Card className="w-full max-w-md p-5" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-3">
          <h2 className="font-heading font-bold">Keyboard shortcuts</h2>
          <Button variant="ghost" size="icon" aria-label="Close" onClick={() => setOpen(false)}>
            <X className="w-4 h-4" />
          </Button>
        </div>
        <dl className="grid grid-cols-[6rem_1fr] gap-y-1.5 text-sm">
          {SHORTCUT_HELP.map((s) => (
            <div key={s.keys} className="contents">
              <dt>
                <kbd className="rounded border border-border bg-secondary px-1.5 py-0.5 font-mono text-xs">{s.keys}</kbd>
              </dt>
              <dd className="text-muted-foreground">{s.what}</dd>
            </div>
          ))}
        </dl>
      </Card>
    </div>
  );
}
