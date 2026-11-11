import { useEffect, useRef } from 'react';

export type Shortcuts = Record<string, (e: KeyboardEvent) => void>;

/** True when the key press belongs to a text field or the editor. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable || target.closest('[contenteditable="true"]')) return true;
  const tag = target.tagName;
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
}

/** The key name used in a shortcut map: "j", "?", "Escape", "/". */
export function keyName(e: KeyboardEvent): string | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  return e.key;
}

/**
 * Single-key shortcuts for the current page (docs: modules/desk/user-guide).
 * Ignored while typing, except Escape.
 */
export function useShortcuts(map: Shortcuts, enabled = true) {
  const ref = useRef(map);
  ref.current = map;
  useEffect(() => {
    if (!enabled) return;
    const onKey = (e: KeyboardEvent) => {
      const k = keyName(e);
      if (!k) return;
      if (k !== 'Escape' && isTyping(e.target)) return;
      const fn = ref.current[k];
      if (fn) {
        e.preventDefault();
        fn(e);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [enabled]);
}

/** Shown by "?" — keep in step with the pages and the user guide. */
export const SHORTCUT_HELP: { keys: string; what: string }[] = [
  { keys: 'j / k', what: 'Next / previous conversation in the list' },
  { keys: 'Enter', what: 'Open the selected conversation' },
  { keys: '/', what: 'Search' },
  { keys: 'c', what: 'New conversation' },
  { keys: 'r', what: 'Reply (in a conversation)' },
  { keys: 'n', what: 'Internal note (in a conversation)' },
  { keys: 'a', what: 'Assign to me' },
  { keys: 'x', what: 'Close the conversation' },
  { keys: 's', what: 'Star or unstar' },
  { keys: 'u', what: 'Back to the inbox' },
  { keys: '?', what: 'Show these shortcuts' },
  { keys: 'Escape', what: 'Close a dialog or leave the editor' },
];
