-- The groups a person last signed in with, in both forms groupmatch stores:
-- groups is the normalised name (comparisons and background jobs), and
-- groups_display is the name as the token sent it (trimmed). The prefix and
-- the match mode are applied only when comparing.

-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS groups_display TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS groups_display;
