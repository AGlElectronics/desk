-- Desk — initial schema.
--
-- Scope: the MVP slice — organizations, users keyed by the OIDC
-- (issuer, subject) pair, mailboxes with IMAP/SMTP settings, customers,
-- conversations made of threads, inbound attachments, the Message-ID index
-- used for threading, viewer heartbeats, and the Den pairing row.
-- Attachments live in Postgres in this slice; S3 comes with uploads
-- (docs/plan.md).

-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;
-- +goose StatementEnd

CREATE TABLE organizations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug         TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (slug = lower(slug) AND slug ~ '^[a-z0-9][a-z0-9-]*[a-z0-9]$')
);

-- Roles are NOT stored here: they come from IdP groups via the access
-- config on every request.
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    email         TEXT,
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (issuer, subject)
);

CREATE INDEX users_email_lower_idx ON users (lower(email));

-- ----------------------------------------------------------------------------
-- MAILBOXES
-- ----------------------------------------------------------------------------
-- Passwords are AES-GCM encrypted with DESK_SECRET_KEY. An empty smtp_host
-- sends through the module-wide SMTP_* settings (Mailpit in dev).

CREATE TABLE mailboxes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name              TEXT NOT NULL,
    email             CITEXT NOT NULL UNIQUE,
    signature         TEXT NOT NULL DEFAULT '',
    imap_host         TEXT NOT NULL DEFAULT '',
    imap_port         INTEGER NOT NULL DEFAULT 993,
    imap_security     TEXT NOT NULL DEFAULT 'tls' CHECK (imap_security IN ('tls', 'starttls', 'none')),
    imap_username     TEXT NOT NULL DEFAULT '',
    imap_password_enc TEXT NOT NULL DEFAULT '',
    smtp_host         TEXT NOT NULL DEFAULT '',
    smtp_port         INTEGER NOT NULL DEFAULT 587,
    smtp_security     TEXT NOT NULL DEFAULT 'starttls' CHECK (smtp_security IN ('tls', 'starttls', 'none')),
    smtp_username     TEXT NOT NULL DEFAULT '',
    smtp_password_enc TEXT NOT NULL DEFAULT '',
    next_number       INTEGER NOT NULL DEFAULT 1,
    last_uid          BIGINT NOT NULL DEFAULT 0,
    uid_validity      BIGINT NOT NULL DEFAULT 0,
    last_fetch_at     TIMESTAMPTZ,
    last_fetch_error  TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ----------------------------------------------------------------------------
-- CUSTOMERS
-- ----------------------------------------------------------------------------

CREATE TABLE customers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    email           CITEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, email)
);

-- ----------------------------------------------------------------------------
-- CONVERSATIONS / THREADS
-- ----------------------------------------------------------------------------

CREATE TABLE conversations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    mailbox_id       UUID NOT NULL REFERENCES mailboxes(id) ON DELETE CASCADE,
    number           INTEGER NOT NULL,
    subject          TEXT NOT NULL DEFAULT '',
    customer_id      UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'closed', 'spam')),
    assignee_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    preview          TEXT NOT NULL DEFAULT '',
    last_activity_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at        TIMESTAMPTZ,
    UNIQUE (mailbox_id, number)
);

CREATE INDEX conversations_folder_idx ON conversations (mailbox_id, status, assignee_id, last_activity_at DESC);
CREATE INDEX conversations_customer_idx ON conversations (customer_id);

CREATE TABLE threads (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('customer', 'reply', 'note', 'system')),
    author_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    from_email      TEXT NOT NULL DEFAULT '',
    to_emails       TEXT[] NOT NULL DEFAULT '{}',
    cc_emails       TEXT[] NOT NULL DEFAULT '{}',
    -- Sanitised HTML (inbound) or escaped text as HTML (replies/notes).
    body_html       TEXT NOT NULL DEFAULT '',
    body_text       TEXT NOT NULL DEFAULT '',
    message_id      TEXT NOT NULL DEFAULT '',
    -- Outbound replies only: queued → sent | failed.
    send_status     TEXT CHECK (send_status IN ('queued', 'sent', 'failed')),
    send_attempts   INTEGER NOT NULL DEFAULT 0,
    send_error      TEXT NOT NULL DEFAULT '',
    next_attempt_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX threads_conversation_idx ON threads (conversation_id, created_at);
CREATE INDEX threads_queue_idx ON threads (next_attempt_at) WHERE send_status = 'queued';

CREATE TABLE attachments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id    UUID NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    filename     TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes   INTEGER NOT NULL,
    content      BYTEA NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX attachments_thread_idx ON attachments (thread_id);

-- Every Message-ID Desk sent or imported, for In-Reply-To / References
-- threading and for dropping duplicates on re-fetch.
CREATE TABLE message_ids (
    message_id      TEXT PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE
);

-- "Alice is replying": heartbeats; rows older than 30 s are ignored.
CREATE TABLE viewers (
    conversation_id UUID NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mode            TEXT NOT NULL CHECK (mode IN ('viewing', 'replying')),
    seen_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (conversation_id, user_id)
);

-- ----------------------------------------------------------------------------
-- DEN PAIRING
-- ----------------------------------------------------------------------------

CREATE TABLE den_link (
    id            SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    den_base_url  TEXT NOT NULL,
    credential    TEXT NOT NULL,
    module_id     TEXT NOT NULL DEFAULT '',
    paired_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    synced_at     TIMESTAMPTZ,
    last_error    TEXT NOT NULL DEFAULT '',
    bundle        JSONB
);

INSERT INTO organizations (id, slug, display_name)
VALUES ('00000000-0000-0000-0000-000000000010', 'demo-org', 'Demo Organization')
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS den_link;
DROP TABLE IF EXISTS viewers;
DROP TABLE IF EXISTS message_ids;
DROP TABLE IF EXISTS attachments;
DROP TABLE IF EXISTS threads;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS customers;
DROP TABLE IF EXISTS mailboxes;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;
