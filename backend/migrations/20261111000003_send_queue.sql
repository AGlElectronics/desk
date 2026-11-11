-- Desk — reply queue claims and outgoing thread kinds.
--
-- The sender claims due replies with FOR UPDATE SKIP LOCKED and marks them
-- 'sending' while it talks to the mail server, so a second process (or a
-- slow run overlapping the next tick) never sends the same reply twice.
-- A claim older than 15 minutes (crash mid-send) goes back to the queue.
--
-- New outgoing kinds: 'forward' (a message forwarded to someone else, with
-- the original's attachments) and 'auto_reply' (the mailbox's automatic
-- answer to a new conversation). provider_message_id is the message's id
-- at the mail provider (Gmail), used to label or trash it there.

-- +goose Up
ALTER TABLE threads DROP CONSTRAINT IF EXISTS threads_send_status_check;
ALTER TABLE threads DROP CONSTRAINT IF EXISTS threads_kind_check;
ALTER TABLE threads
    ADD CONSTRAINT threads_send_status_check CHECK (send_status IN ('queued', 'sending', 'sent', 'failed')),
    ADD CONSTRAINT threads_kind_check CHECK (kind IN ('customer', 'reply', 'note', 'system', 'forward', 'auto_reply')),
    ADD COLUMN claimed_at          TIMESTAMPTZ,
    ADD COLUMN provider_message_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN forward_of          UUID REFERENCES threads(id) ON DELETE SET NULL;

CREATE INDEX threads_provider_idx ON threads (provider_message_id) WHERE provider_message_id <> '';

-- +goose Down
DROP INDEX IF EXISTS threads_provider_idx;
UPDATE threads SET send_status = 'queued' WHERE send_status = 'sending';
DELETE FROM threads WHERE kind IN ('forward', 'auto_reply');
ALTER TABLE threads DROP COLUMN IF EXISTS forward_of, DROP COLUMN IF EXISTS provider_message_id,
    DROP COLUMN IF EXISTS claimed_at, DROP CONSTRAINT IF EXISTS threads_kind_check,
    DROP CONSTRAINT IF EXISTS threads_send_status_check;
ALTER TABLE threads
    ADD CONSTRAINT threads_send_status_check CHECK (send_status IN ('queued', 'sent', 'failed')),
    ADD CONSTRAINT threads_kind_check CHECK (kind IN ('customer', 'reply', 'note', 'system'));
