import { fireEvent, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { TagEditor } from './TagEditor';

describe('TagEditor', () => {
  it('adds a lower-cased tag on Enter and skips duplicates', () => {
    const onChange = vi.fn();
    const { getByLabelText } = render(<TagEditor tags={['vip']} onChange={onChange} />);
    const input = getByLabelText('Add tag');
    fireEvent.change(input, { target: { value: ' Refund ' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(['vip', 'refund']);
    onChange.mockClear();
    fireEvent.change(input, { target: { value: 'VIP,' } });
    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes a tag', () => {
    const onChange = vi.fn();
    const { getByLabelText } = render(<TagEditor tags={['vip', 'refund']} onChange={onChange} />);
    fireEvent.click(getByLabelText('Remove tag vip'));
    expect(onChange).toHaveBeenCalledWith(['refund']);
  });

  it('is read-only when disabled', () => {
    const { queryByLabelText, getByText } = render(<TagEditor tags={['vip']} disabled onChange={() => {}} />);
    expect(getByText('vip')).toBeInTheDocument();
    expect(queryByLabelText('Add tag')).toBeNull();
    expect(queryByLabelText('Remove tag vip')).toBeNull();
  });
});
