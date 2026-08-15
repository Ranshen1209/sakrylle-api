-- Migration 165: Re-add users.email_verified (idempotent), repairing migration 157.
-- Created: 2026-06-13
-- Reason: Migration 157 was authored with sql-migrate-style `-- +migrate Up` /
--         `-- +migrate Down` directives, but this repo's custom migration runner
--         (internal/repository/migrations_runner.go) does NOT parse those
--         directives — it splits the whole file on ';' and executes EVERY
--         statement. So 157 ran both its `ADD COLUMN ... email_verified` AND its
--         "Down" `DROP COLUMN ... email_verified`, leaving any freshly-built DB
--         without the column. Confirmed: integration CI fails with
--         `column "email_verified" of relation "users" does not exist`, and a
--         disaster-recovery rebuild from migrations would silently lose the
--         OIDC email_verified claim column. Production's existing column predates
--         this and is unaffected.
--
--         This migration idempotently re-adds the column so fresh rebuilds and
--         integration get it; it is a no-op where the column already exists
--         (production, dev). We intentionally do NOT edit 157 to avoid a
--         checksum-compatibility change to an already-applied migration.

ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT false;
