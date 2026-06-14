-- Migration 166: Grant account:balance:read scope to sakrylle-web
-- Created: 2026-06-15
-- Reason: Persist an out-of-band psql edit so it survives a fresh DB rebuild.
--         sakrylle-web (chat.sakrylle.com, Open WebUI RP) requests
--         account:balance:read at authorize time to power the Sidebar
--         balance/recharge widget (GET /v1/account/balance with the user's
--         own OIDC token). Without this scope in allowed_scopes the authorize
--         call fails closed with invalid_scope and SSO login breaks entirely.
--         (The /v1/account/balance endpoint already accepts account:read too,
--         but the RP requests the narrower account:balance:read by design.)

-- Append account:balance:read to allowed_scopes if not already present.
UPDATE oauth_clients
SET allowed_scopes = allowed_scopes || '["account:balance:read"]'::jsonb,
    updated_at = now()
WHERE client_id = 'sakrylle-web'
  AND NOT (allowed_scopes ? 'account:balance:read');

-- Verify the update
SELECT
    client_id,
    allowed_scopes
FROM oauth_clients
WHERE client_id = 'sakrylle-web';
