-- 155_oidc_request_uris
-- Purpose: Add request_uris column to oauth_clients for OIDC Core §6
-- (request_uri parameter support).
--
-- request_uris is a JSONB array of pre-registered HTTPS URIs from which
-- the client's request objects may be fetched. Each entry MUST use the
-- https scheme (enforced at the application layer).
--
-- Idempotent: safe to re-run against a database that already has this column.
-- References: sakrylle-docs/03-sakrylle-api-oidc-architecture.md §6

-- 10.1: Add request_uris to oauth_clients
ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS request_uris JSONB DEFAULT '[]';
