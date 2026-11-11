-- Desk — attachments and raw mail in S3.
--
-- Attachment content moves out of PostgreSQL into the S3 bucket
-- (S3_BUCKET); the row keeps the metadata and the object key. Every
-- imported message also keeps its raw RFC 5322 copy in S3 (raw_key), so
-- nothing is lost to parsing and retention can purge raw mail separately.
-- Development databases from before this migration lose stored attachment
-- content (nothing was released with it).

-- +goose Up
ALTER TABLE attachments
    DROP COLUMN content,
    ADD COLUMN storage_key TEXT NOT NULL DEFAULT '',
    -- Content-ID of an inline part ("image001.png@01D..."), for cid: links.
    ADD COLUMN content_id  TEXT NOT NULL DEFAULT '',
    ADD COLUMN inline      BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE threads
    ADD COLUMN raw_key        TEXT NOT NULL DEFAULT '',
    ADD COLUMN raw_size_bytes BIGINT NOT NULL DEFAULT 0,
    -- Auto-replies and bounces: shown in the conversation but never reopen it.
    ADD COLUMN auto_submitted BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE threads DROP COLUMN IF EXISTS auto_submitted, DROP COLUMN IF EXISTS raw_size_bytes,
    DROP COLUMN IF EXISTS raw_key;
ALTER TABLE attachments DROP COLUMN IF EXISTS inline, DROP COLUMN IF EXISTS content_id,
    DROP COLUMN IF EXISTS storage_key, ADD COLUMN content BYTEA NOT NULL DEFAULT ''::bytea;
