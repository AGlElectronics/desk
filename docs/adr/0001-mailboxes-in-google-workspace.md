# ADR 0001 — Mailboxes live in Google Workspace; IMAP/SMTP is the second connector

Status: accepted (pre-1.0.0, maintainer decision 2026-11-11).

## Context

Desk needs real mailboxes such as `info@` and `registration@`. Running a mail
server ourselves (spam filtering, DKIM, deliverability, storage) is work no
convention team wants. Most organisations that use Con Services already have
Google Workspace. Gmail refuses plain IMAP passwords for Workspace accounts,
so the MVP's password login would not work there anyway.

## Decision

Every mailbox has a **connector**, chosen when it is created and never
changed afterwards:

| Connector | What it is in Google | How Desk reads and sends |
| --- | --- | --- |
| `google_user` | Its own licensed Workspace user (`info@example.org`). | Gmail API as that user (domain-wide delegation). |
| `google_group` | A free group/alias whose only member is one shared licensed account (`GOOGLE_SHARED_MAILBOX`, e.g. `desk@example.org`). | Desk reads the shared account once for all group mailboxes, routes each message by recipient (`Delivered-To`, `X-Original-To`, `To`, `Cc`) and replies "send as" the group address. Mail for no known mailbox gets the label `<GOOGLE_LABEL>/Unrouted`. |
| `imap` | Not in Google: any IMAP/SMTP server. | IMAP polling and SMTP, as in the MVP. For standalone installations without Workspace. |

Creating a Google mailbox in Desk **creates it in Google** (user, or group
plus send-as alias). The Desk row is written first, so a failed Google call
leaves a mailbox the admin can see, fix and provision again
(`POST /api/v1/mailboxes/{id}/provision`).

Desk talks to Google only through the small interface in
`internal/mailprovider` (create/suspend/restore/delete a mailbox; list,
get, label, send, trash and untrash mail). Desk is built and tested against
the in-memory `Fake`. The real Gmail API / Admin SDK client is shared by
several modules and is built in `shared/go/gworkspace`; it is plugged in
through `app.GoogleProvider`. Until that client is wired in, a Desk build
refuses to start with `GOOGLE_WORKSPACE_DOMAIN` set unless `GOOGLE_FAKE=true`
(development only).

## Consequences

- No mail server to run. Deliverability, spam filtering and backups of the
  original mail are Google's job.
- Per mailbox the admin trades cost against isolation: `google_user` costs a
  licence but keeps the mailbox separate in Google; `google_group` is free
  but every group mailbox shares one account's storage and quota.
- Desk needs a service account with domain-wide delegation and the scopes
  listed in the Google Workspace setup page, plus network egress to
  `gmail.googleapis.com`, `admin.googleapis.com` and `oauth2.googleapis.com`.
- IMAP/SMTP stays fully supported, with the host and plaintext rules of
  [ADR 0004](0004-mail-host-safety.md).
- Microsoft 365 OAuth2 is not part of 1.0.0.
