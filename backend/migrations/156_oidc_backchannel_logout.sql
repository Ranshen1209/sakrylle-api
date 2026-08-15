-- 156_oidc_backchannel_logout
-- Purpose: Add backchannel_logout_uri and backchannel_logout_session_required
-- columns to oauth_clients for OIDC Back-Channel Logout 1.0 support.
--
-- backchannel_logout_uri: When set, the OP POSTs a logout_token JWT to this
-- URI when the user logs out. NULL means no back-channel notification.
-- backchannel_logout_session_required: When true, the OP includes a sid
-- claim in the id_token so the RP can associate the session.
--
-- Idempotent: safe to re-run against a database that already has these columns.
-- References: sakrylle-docs/03-sakrylle-api-oidc-architecture.md §C

-- 10.1: Extend oauth_clients with back-channel logout support
ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS backchannel_logout_uri TEXT;

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS backchannel_logout_session_required BOOLEAN NOT NULL DEFAULT FALSE;
