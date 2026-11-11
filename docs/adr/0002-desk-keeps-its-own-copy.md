# ADR 0002 — Desk keeps its own copy; handled mail is marked at the source

Status: accepted (pre-1.0.0).

## Context

Desk reads mail from Google Workspace or an IMAP server. It could either
work directly on the provider's copy (every view a provider call) or import
each message once and keep its own copy. The MVP stored attachments as
`bytea` in Postgres, which makes the database large and backups slow.

## Decision

- Desk **imports** every message once and keeps its own copy:
  conversations, threads (sanitised HTML and plain text), customers and
  metadata in **Postgres**; attachments, inline images and the raw `.eml`
  in the module's **S3 bucket** (`S3_BUCKET`, `desk-data` on the platform).
  `S3_*` settings are required; nothing is stored as `bytea` any more.
- Keys: `raw/<mailbox>/<yyyy-mm>/<uuid>.eml` and
  `files/<conversation>/<uuid>`. Rows are deleted before their files, so a
  failure leaves an orphan object, never a row without its file.
- The bucket goes into the Den operator's `FILES_S3_BUCKETS`, so Den's
  backups include it.
- Handled mail is **marked** at the source, never deleted by the import:
  - Gmail: the label `GOOGLE_LABEL` (default `Desk`). Desk only reads inbox
    messages without that label, so the label is also the import cursor.
  - IMAP: the flag `\Seen`, plus an optional per-mailbox "move to folder"
    (`imap_move_to`) after import. The UID cursor (`last_uid`,
    `uid_validity`) stays the import cursor.
- A message that cannot be parsed or stored is recorded in
  `import_failures` and skipped, so one bad message never stalls a mailbox.

## Consequences

- Desk works when the provider is slow or down, search runs in Postgres,
  and retention ([ADR 0005](0005-retention.md)) is Desk's to apply.
- Mail exists twice (provider and Desk). Deleting in Desk also trashes the
  provider's copy ([ADR 0006](0006-trash-and-purge.md)); retention in Desk
  does not touch the provider's copy.
- Removing the `Desk` label in Gmail makes Desk import the message again;
  the `Message-ID` dedupe keeps that harmless.
