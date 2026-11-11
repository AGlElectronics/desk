-- Desk — Google Workspace connectors, mailbox trash, settings, outbox, audit.
--
-- connector says where a mailbox's mail lives (docs/adr/0001):
--   imap          own IMAP/SMTP server (standalone, no Workspace)
--   google_user   its own licensed Workspace user; Desk acts as that user
--   google_group  a free group delivering into the shared account
--                 (GOOGLE_SHARED_MAILBOX); Desk routes by recipient and
--                 sends "as" the group address
-- provider_state follows the mailbox in Google: provisioning → active,
-- suspended while the mailbox is in Desk's trash, error when a call failed.
--
-- Deleting a mailbox puts it in Desk's trash (deleted_at) and suspends it
-- in Google; it is purged for good at purge_after (daily job, reminders to
-- Desk admins before) or when an admin purges it.

-- +goose Up
ALTER TABLE mailboxes
    ADD COLUMN connector            TEXT NOT NULL DEFAULT 'imap'
        CHECK (connector IN ('imap', 'google_user', 'google_group')),
    ADD COLUMN provider_state       TEXT NOT NULL DEFAULT ''
        CHECK (provider_state IN ('', 'provisioning', 'active', 'suspended', 'error')),
    ADD COLUMN provider_error       TEXT NOT NULL DEFAULT '',
    ADD COLUMN deleted_at           TIMESTAMPTZ,
    ADD COLUMN deleted_by           UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN purge_after          TIMESTAMPTZ,
    -- How many purge reminders went out (7 days and 1 day before).
    ADD COLUMN purge_reminders_sent INTEGER NOT NULL DEFAULT 0;

CREATE INDEX mailboxes_purge_idx ON mailboxes (purge_after) WHERE deleted_at IS NOT NULL;

-- Who is a Desk admin and which groups a user had at their last request,
-- so background jobs (reminders, notifications) know whom to email.
ALTER TABLE users
    ADD COLUMN groups     TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN desk_admin BOOLEAN NOT NULL DEFAULT false;

-- Module settings (standalone values; Den's retention overrides them).
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Desk's own emails (purge reminders, notifications), sent by the sender
-- loop with retries.
CREATE TABLE outbox (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    to_email        TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body_text       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at      TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX outbox_queue_idx ON outbox (next_attempt_at) WHERE status = 'queued';

-- Who did what. Kept when the mailbox or conversation is gone, so ids are
-- plain columns, not foreign keys.
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    actor_id    UUID,
    actor_name  TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL,
    mailbox_id  UUID,
    target_type TEXT NOT NULL DEFAULT '',
    target_id   TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    details     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_mailbox_idx ON audit_log (mailbox_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS settings;
ALTER TABLE users DROP COLUMN IF EXISTS desk_admin, DROP COLUMN IF EXISTS groups;
DROP INDEX IF EXISTS mailboxes_purge_idx;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS purge_reminders_sent, DROP COLUMN IF EXISTS purge_after,
    DROP COLUMN IF EXISTS deleted_by, DROP COLUMN IF EXISTS deleted_at, DROP COLUMN IF EXISTS provider_error,
    DROP COLUMN IF EXISTS provider_state, DROP COLUMN IF EXISTS connector;
