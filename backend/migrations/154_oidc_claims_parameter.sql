-- 154_oidc_claims_parameter
-- Purpose: Add claims column to oauth_authorize_transactions for OIDC Core §5.5
-- voluntary claims request support.
--
-- Idempotent: safe to re-run against a database that already has this column.
-- References: sakrylle-docs/03-sakrylle-api-oidc-architecture.md §5

-- 10.3: Extend oauth_authorize_transactions with claims storage
ALTER TABLE oauth_authorize_transactions
    ADD COLUMN IF NOT EXISTS claims JSONB;
