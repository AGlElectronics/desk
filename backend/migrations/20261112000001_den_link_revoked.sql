-- Desk — the shared denlink package records when Den rejects the module's
-- credential (denlink.SchemaUpgrade). Without this column every pairing
-- query fails.

-- +goose Up
ALTER TABLE den_link ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE den_link DROP COLUMN IF EXISTS revoked_at;
