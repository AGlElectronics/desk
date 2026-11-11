-- Desk — fetch back-off and import failures.
--
-- A mailbox that keeps failing (wrong password, server down) is polled
-- less and less often instead of every FETCH_INTERVAL, so the provider does
-- not lock the account and one broken mailbox does not hold up the others.
-- A message that cannot be imported is recorded here and skipped, so it no
-- longer stalls the whole mailbox.

-- +goose Up
ALTER TABLE mailboxes
    ADD COLUMN fetch_failures INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN next_fetch_at  TIMESTAMPTZ,
    -- IMAP folder handled mail is moved to ('' = leave it in INBOX, \Seen).
    ADD COLUMN imap_move_to   TEXT NOT NULL DEFAULT '';

CREATE TABLE import_failures (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    -- "imap:<uidvalidity>:<uid>" or "gmail:<message id>".
    source     TEXT NOT NULL,
    subject    TEXT NOT NULL DEFAULT '',
    from_email TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    error      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mailbox_id, source)
);

-- +goose Down
DROP TABLE IF EXISTS import_failures;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS imap_move_to, DROP COLUMN IF EXISTS next_fetch_at,
    DROP COLUMN IF EXISTS fetch_failures;
