-- Desk — trash, tags, stars, saved replies, auto-reply, customer emails,
-- notifications and full-text search.

-- +goose Up

-- Desk's trash for emails (threads) and whole conversations. Deleting also
-- moves the message to Gmail's trash; both are recoverable until purged.
ALTER TABLE conversations
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN search     tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(subject, ''))) STORED;
ALTER TABLE threads
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN search     tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(body_text, ''))) STORED;

CREATE INDEX conversations_trash_idx ON conversations (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX threads_trash_idx ON threads (deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX conversations_search_idx ON conversations USING gin (search);
CREATE INDEX threads_search_idx ON threads USING gin (search);
CREATE INDEX conversations_list_idx ON conversations (last_activity_at DESC, id DESC);

CREATE TABLE conversation_tags (
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    tag             TEXT NOT NULL CHECK (tag ~ '^[a-z0-9][a-z0-9 _-]{0,31}$'),
    PRIMARY KEY (conversation_id, tag)
);
CREATE INDEX conversation_tags_tag_idx ON conversation_tags (tag);

-- Stars are personal.
CREATE TABLE conversation_stars (
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);

-- Saved replies: for one mailbox, or every mailbox (mailbox_id NULL).
CREATE TABLE saved_replies (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id UUID REFERENCES mailboxes(id) ON DELETE CASCADE,
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    body_html  TEXT NOT NULL,
    body_text  TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX saved_replies_mailbox_idx ON saved_replies (mailbox_id, name);

-- Automatic answer to the first message of a new conversation.
ALTER TABLE mailboxes
    ADD COLUMN auto_reply_enabled BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN auto_reply_subject TEXT NOT NULL DEFAULT '',
    ADD COLUMN auto_reply_html    TEXT NOT NULL DEFAULT '',
    ADD COLUMN auto_reply_text    TEXT NOT NULL DEFAULT '';

-- A customer may write from several addresses.
CREATE TABLE customer_emails (
    customer_id     UUID NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    email           CITEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, email)
);
CREATE INDEX customer_emails_customer_idx ON customer_emails (customer_id);
INSERT INTO customer_emails (customer_id, organization_id, email)
SELECT id, organization_id, email FROM customers ON CONFLICT DO NOTHING;

-- Email notifications per user (off by default).
CREATE TABLE notification_prefs (
    user_id          UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    new_conversation BOOLEAN NOT NULL DEFAULT false,
    assigned_to_me   BOOLEAN NOT NULL DEFAULT false,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS notification_prefs;
DROP TABLE IF EXISTS customer_emails;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS auto_reply_text, DROP COLUMN IF EXISTS auto_reply_html,
    DROP COLUMN IF EXISTS auto_reply_subject, DROP COLUMN IF EXISTS auto_reply_enabled;
DROP TABLE IF EXISTS saved_replies;
DROP TABLE IF EXISTS conversation_stars;
DROP TABLE IF EXISTS conversation_tags;
DROP INDEX IF EXISTS conversations_list_idx;
ALTER TABLE threads DROP COLUMN IF EXISTS search, DROP COLUMN IF EXISTS deleted_by, DROP COLUMN IF EXISTS deleted_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS search, DROP COLUMN IF EXISTS deleted_by, DROP COLUMN IF EXISTS deleted_at;
