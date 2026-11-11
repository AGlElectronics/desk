# ADR 0005 — Retention comes from Den's module settings, Desk's own as fallback

Status: accepted (pre-1.0.0, maintainer decision). The default periods for
closed conversations, attachments and raw mail are **pending legal review**.

## Context

Support mail contains personal data (GDPR). How long to keep it is a legal
decision per organisation, not a code default. When Desk is paired with
Den, the organisation manages module settings in one place (Den's module
settings); a standalone Desk needs its own.

## Decision

Five periods, in days, each `null` = keep:

| Field | What is deleted after it | Default |
| --- | --- | --- |
| `closed_conversation_days` | Closed conversations with their threads, attachments and raw mail, counted from closing. | keep (pending legal review) |
| `attachment_days` | Attachments, counted from arrival. | keep (pending legal review) |
| `raw_mail_days` | The raw `.eml` copies, counted from arrival. | keep (pending legal review) |
| `trash_days` | Emails and conversations in Desk's trash. | 30 |
| `mailbox_trash_days` | Mailboxes in Desk's trash (then purged, [ADR 0006](0006-trash-and-purge.md)). | 30 |

- Den sends `retention` in the integration bundle (flat, or per module under
  `desk`). Every field Den sets wins and is read-only in Desk
  (`managed_by: {"field": "den"}`).
- Fields Den does not set come from Desk's own setting
  (`PUT /api/v1/settings/retention`, Desk admins), then from the defaults.
- A daily job applies the periods, deleting rows first and files after.
  Each run that removes something writes an audit entry.
- The settings API lists the fields still on "keep by default" in
  `pending_legal_review`, and the web app shows that as a warning.

## Consequences

- Nothing personal is deleted until someone decides a period.
- Retention in Desk does not delete the provider's copy (Gmail, IMAP);
  that follows the provider's own retention.
