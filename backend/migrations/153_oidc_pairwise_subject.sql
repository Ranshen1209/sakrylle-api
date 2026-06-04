-- 153_oidc_pairwise_subject
-- Purpose: Add subject_type and sector_identifier_uri columns to oauth_clients
-- for OIDC pairwise subject type support (OIDC Core §8).
--
-- Idempotent: safe to re-run against a database that already has these columns.
-- References: sakrylle-docs/03-sakrylle-api-oidc-architecture.md §8

-- 10.1: Extend oauth_clients with pairwise subject fields
ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS subject_type VARCHAR(16) NOT NULL DEFAULT 'public';

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS sector_identifier_uri TEXT;

-- Add CHECK constraint for subject_type values
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_clients_subject_type_check'
    ) THEN
        ALTER TABLE oauth_clients
            ADD CONSTRAINT oauth_clients_subject_type_check
            CHECK (subject_type IN ('public', 'pairwise'));
    END IF;
END$$;
