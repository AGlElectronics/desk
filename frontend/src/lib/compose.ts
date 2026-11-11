import type { ConversationDetail } from '@/api/desk';

/** "Reply all": who the customer's last message also went to. */
export function defaultCC(c: Pick<ConversationDetail, 'threads' | 'customer'>, mailboxEmail: string): string[] {
  const last = [...c.threads].reverse().find((t) => t.kind === 'customer' && !t.auto_submitted && !t.deleted_at);
  if (!last) return [];
  const skip = new Set([mailboxEmail.toLowerCase(), c.customer.email.toLowerCase()]);
  const out: string[] = [];
  for (const a of [...last.to, ...last.cc]) {
    const l = a.toLowerCase();
    if (!skip.has(l)) {
      skip.add(l);
      out.push(l);
    }
  }
  return out;
}

/** Addresses typed into a field, separated by commas, semicolons or spaces. */
export function splitAddresses(s: string): string[] {
  return s
    .split(/[,;\s]+/)
    .map((x) => x.trim())
    .filter(Boolean);
}
