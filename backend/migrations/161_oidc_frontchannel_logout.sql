-- 161_oidc_frontchannel_logout
-- Purpose: Add frontchannel_logout_uri column to oauth_clients for
-- OIDC Front-Channel Logout 1.0 support.
--
-- frontchannel_logout_uri: When set, the OP renders a hidden iframe
-- pointing at this URI during front-channel logout so the RP can clear
-- its own session state. NULL means no front-channel notification.
--
-- Idempotent: safe to re-run against a database that already has this column.
-- References: OpenID Connect Front-Channel Logout 1.0

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS frontchannel_logout_uri TEXT;
