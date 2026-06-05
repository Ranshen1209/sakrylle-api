-- +migrate Up
-- Add per-user email_verified flag for OIDC id_token and UserInfo claims.
-- Default false: existing users are unverified until explicitly marked.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;

-- +migrate Down
ALTER TABLE users DROP COLUMN IF EXISTS email_verified;
