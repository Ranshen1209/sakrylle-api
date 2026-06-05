-- 160_oidc_introspect_client_confidential
-- Purpose: Add client_confidential flag to oauth_clients for RFC 7662
-- Token Introspection. Only confidential clients (those with a
-- client_secret_hash) may call POST /oauth/introspect.
--
-- Idempotent: safe to re-run against a database that already has this column.

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS client_confidential BOOLEAN NOT NULL DEFAULT false;

-- Mark known confidential clients (those with a non-empty client_secret_hash).
UPDATE oauth_clients
    SET client_confidential = true
    WHERE client_secret_hash IS NOT NULL AND client_secret_hash != '';
