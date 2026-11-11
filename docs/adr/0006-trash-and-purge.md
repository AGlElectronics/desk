# ADR 0006 — Deleting goes to a trash first: emails, conversations and mailboxes

Status: accepted (pre-1.0.0, maintainer decision). Same flow as HR's
offboarding: soft delete, reminders, automatic purge.

## Context

The MVP deleted a mailbox with every conversation and attachment in one
click, with no audit. Deleting a single email was not possible at all.

## Decision

**Emails and conversations** (needs the mailbox's `delete` role):

- Deleting moves the email or conversation to **Desk's trash** and, for
  Google mailboxes, the message to **Gmail's trash** (Gmail keeps it 30
  days). Restoring undoes both. If Google fails, Desk's trash still
  applies and the response carries a `warning`.
- Queued replies in a deleted conversation are cancelled.
- The trash is emptied after `trash_days` ([ADR 0005](0005-retention.md)).
- Every delete and restore is in the audit log.

**Mailboxes** (Desk admins only):

- Deleting moves the mailbox to **Desk's mailbox trash**: it stops fetching
  and sending, queued replies fail with "the mailbox was deleted", and a
  Google mailbox is **suspended** (user suspended; group stops delivering,
  send-as alias removed), not deleted.
- Restoring resumes it, also in Google.
- After `mailbox_trash_days` a daily job **purges** it: deleted in Google,
  its files in S3, then every row in Desk. Admins can purge earlier.
- Desk admins of the mailbox's organization get **email reminders** 7 days
  and 1 day before the automatic purge (Desk's outbox, `NOTIFY_FROM`, with
  retries).
- Creation, changes, trash, restore, provision, purge and reminders are in
  the audit log (`GET /api/v1/audit`).

## Consequences

- A misclick costs nothing for 30 days; a purge cannot be undone.
- The mailbox's address stays taken (also in Desk's trash) until purge.
- Without `NOTIFY_FROM` and a way to send (module SMTP or the Google shared
  account), reminders are not sent; the purge still happens.
