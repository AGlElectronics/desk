import { describe, expect, it } from 'vitest';
import type { Thread } from '@/api/desk';
import { defaultCC, splitAddresses } from './compose';

function thread(p: Partial<Thread>): Thread {
  return {
    id: crypto.randomUUID(),
    kind: 'customer',
    from_email: 'alice@example.org',
    to: [],
    cc: [],
    body_html: '',
    body_text: '',
    auto_submitted: false,
    attachments: [],
    created_at: '2026-11-01T10:00:00Z',
    ...p,
  };
}

const customer = { id: 'c1', email: 'alice@example.org', name: 'Alice', notes: '' };

describe('defaultCC', () => {
  it('copies the other recipients of the last customer message', () => {
    const threads = [
      thread({ to: ['info@con.example'], cc: ['old@example.org'] }),
      thread({ kind: 'reply', from_email: 'info@con.example', to: ['alice@example.org'] }),
      thread({ to: ['Info@con.example', 'Bob@example.org'], cc: ['carol@example.org', 'ALICE@example.org', 'bob@example.org'] }),
    ];
    expect(defaultCC({ threads, customer }, 'info@con.example')).toEqual(['bob@example.org', 'carol@example.org']);
  });

  it('skips auto-replies and deleted messages', () => {
    const threads = [
      thread({ cc: ['real@example.org'] }),
      thread({ cc: ['bounce@example.org'], auto_submitted: true }),
      thread({ cc: ['gone@example.org'], deleted_at: '2026-11-02T00:00:00Z' }),
    ];
    expect(defaultCC({ threads, customer }, 'info@con.example')).toEqual(['real@example.org']);
  });

  it('is empty without a customer message', () => {
    expect(defaultCC({ threads: [thread({ kind: 'note' })], customer }, 'info@con.example')).toEqual([]);
  });
});

describe('splitAddresses', () => {
  it('splits on commas, semicolons and spaces', () => {
    expect(splitAddresses(' a@x.org, b@x.org;c@x.org  d@x.org ,')).toEqual(['a@x.org', 'b@x.org', 'c@x.org', 'd@x.org']);
  });
});
