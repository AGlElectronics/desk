import { fireEvent, render, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { isTyping, useShortcuts } from './useShortcuts';

describe('useShortcuts', () => {
  it('runs the mapped key and ignores modifier combinations', () => {
    const next = vi.fn();
    renderHook(() => useShortcuts({ j: next }));
    fireEvent.keyDown(window, { key: 'j' });
    fireEvent.keyDown(window, { key: 'j', ctrlKey: true });
    expect(next).toHaveBeenCalledOnce();
  });

  it('ignores keys typed into a field, except Escape', () => {
    const reply = vi.fn();
    const escape = vi.fn();
    renderHook(() => useShortcuts({ r: reply, Escape: escape }));
    const { getByRole } = render(<textarea aria-label="Reply" />);
    const field = getByRole('textbox');
    fireEvent.keyDown(field, { key: 'r' });
    fireEvent.keyDown(field, { key: 'Escape' });
    expect(reply).not.toHaveBeenCalled();
    expect(escape).toHaveBeenCalledOnce();
  });

  it('does nothing while disabled', () => {
    const fn = vi.fn();
    renderHook(() => useShortcuts({ x: fn }, false));
    fireEvent.keyDown(window, { key: 'x' });
    expect(fn).not.toHaveBeenCalled();
  });
});

describe('isTyping', () => {
  it('treats the rich editor as a text field', () => {
    const { container } = render(
      <div contentEditable="true" suppressContentEditableWarning>
        <p>text</p>
      </div>
    );
    expect(isTyping(container.querySelector('p'))).toBe(true);
    expect(isTyping(document.body)).toBe(false);
  });
});
