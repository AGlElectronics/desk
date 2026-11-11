import { useState } from 'react';
import { Tag, X } from 'lucide-react';

/** Tags as removable chips plus an input; Enter or comma adds one. */
export function TagEditor({
  tags,
  suggestions = [],
  disabled,
  onChange,
}: {
  tags: string[];
  suggestions?: string[];
  disabled?: boolean;
  onChange: (tags: string[]) => void;
}) {
  const [draft, setDraft] = useState('');
  const add = (raw: string) => {
    const t = raw.trim().toLowerCase();
    if (t && !tags.includes(t)) onChange([...tags, t]);
    setDraft('');
  };
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <Tag className="w-3.5 h-3.5 text-muted-foreground" aria-hidden />
      {tags.map((t) => (
        <span key={t} className="inline-flex items-center gap-1 rounded-pill bg-secondary px-2 py-0.5 text-xs">
          {t}
          {disabled ? null : (
            <button type="button" aria-label={`Remove tag ${t}`} onClick={() => onChange(tags.filter((x) => x !== t))}>
              <X className="w-3 h-3" />
            </button>
          )}
        </span>
      ))}
      {disabled ? null : (
        <>
          <input
            aria-label="Add tag"
            list="desk-tag-suggestions"
            value={draft}
            placeholder="Add tag"
            onChange={(e) => (e.target.value.endsWith(',') ? add(e.target.value.slice(0, -1)) : setDraft(e.target.value))}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                add(draft);
              }
            }}
            onBlur={() => draft && add(draft)}
            className="h-7 w-28 rounded-md border border-input bg-background px-2 text-xs"
          />
          <datalist id="desk-tag-suggestions">
            {suggestions.filter((s) => !tags.includes(s)).map((s) => (
              <option key={s} value={s} />
            ))}
          </datalist>
        </>
      )}
    </div>
  );
}
