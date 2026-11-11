-- Desk — per-mailbox roles.
--
-- Every mailbox has a slug ("info" for info@…). It names the mailbox's three
-- roles in Desk's role catalog and their Keycloak groups (docs/adr/0003):
--   desk_<slug>_viewer   group desk-<slug>-viewer    read conversations
--   desk_<slug>_manager  group desk-<slug>-manager   work them (reply, assign…)
--   desk_<slug>_delete   group desk-<slug>-delete    delete emails and conversations
-- The slug never changes: renaming it would silently drop everyone's access.

-- +goose Up
ALTER TABLE mailboxes ADD COLUMN slug TEXT;

UPDATE mailboxes m SET slug = s.slug
  FROM (SELECT id,
               trim(both '-' FROM regexp_replace(lower(split_part(email::text, '@', 1)), '[^a-z0-9]+', '-', 'g'))
               || CASE WHEN row_number() OVER (PARTITION BY lower(split_part(email::text, '@', 1)) ORDER BY created_at) > 1
                       THEN '-' || row_number() OVER (PARTITION BY lower(split_part(email::text, '@', 1)) ORDER BY created_at)
                       ELSE '' END AS slug
          FROM mailboxes) s
 WHERE s.id = m.id;

ALTER TABLE mailboxes
    ALTER COLUMN slug SET NOT NULL,
    ADD CONSTRAINT mailboxes_slug_key UNIQUE (slug),
    ADD CONSTRAINT mailboxes_slug_check CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');

ALTER TABLE customers ADD COLUMN updated_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE customers DROP COLUMN IF EXISTS updated_by;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS slug;
