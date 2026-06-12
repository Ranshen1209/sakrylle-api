-- Migration 163: Reconcile sakrylle-cli OAuth client to its canonical contract.
-- Created: 2026-06-12
-- Reason: Persist an out-of-band psql registration so it survives a fresh DB
--         rebuild. sakrylle-cli was pruned from production on 2026-06-11 and
--         re-registered by hand on 2026-06-12. The seed in migration 148 left
--         redirect_uris empty ('[]') and predates the OIDC-standard scope set,
--         so a database recreated purely from migrations would seed a BROKEN
--         CLI client: the empty redirect_uris whitelist breaks the RFC 8252
--         loopback authorization-code (browser) login, even though the device
--         flow and the OIDC scopes (added later by migration 152) would work.
--
--         Every statement is additive and guarded by a WHERE clause, so this
--         migration is a no-op on the live production database (already in the
--         target state) and repairs a fresh rebuild. It never removes scopes or
--         URIs an operator may have added.
--
-- NOT set here: default_group_id. Per the migration 148 convention,
--         default_group_id is operator-set per deployment to avoid hard-coding
--         environment-specific group IDs. Sakrylle production binds group 3
--         (GPT-Pro) out-of-band; see sakrylle-docs/10-platform-identity. v2
--         OAuth does NOT consume the global oauth_default_group_id setting and
--         fails closed with invalid_group until a default group is bound, so a
--         fresh deployment must set sakrylle-cli.default_group_id before the CLI
--         can complete a login.

-- ── 1. redirect_uris: ensure both RFC 8252 loopback callbacks are present ────
-- Registered without an explicit port so the provider's loopback matcher
-- (oauth_provider_service.go redirectURIAllowed) accepts any random port the
-- CLI binds. Two paths: /callback (primary) and /auth/callback (legacy compat).
UPDATE oauth_clients SET
    redirect_uris = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.redirect_uris, '[]'::jsonb) ||
            '["http://127.0.0.1/callback","http://127.0.0.1/auth/callback"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-cli'
AND NOT (redirect_uris @> '["http://127.0.0.1/callback","http://127.0.0.1/auth/callback"]'::jsonb);

-- ── 2. allowed_scopes: ensure the canonical CLI contract scopes are present ──
UPDATE oauth_clients SET
    allowed_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.allowed_scopes, '[]'::jsonb) ||
            '["openid","profile","email","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-cli'
AND NOT (allowed_scopes @> '["openid","profile","email","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb);

-- ── 3. default_scopes: same canonical set (used when /authorize omits scope) ─
UPDATE oauth_clients SET
    default_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.default_scopes, '[]'::jsonb) ||
            '["openid","profile","email","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-cli'
AND NOT (default_scopes @> '["openid","profile","email","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb);

-- ── 4. device_flow_enabled: ensure enabled (148 sets TRUE; defensive) ────────
UPDATE oauth_clients SET device_flow_enabled = TRUE, updated_at = NOW()
WHERE client_id = 'sakrylle-cli' AND device_flow_enabled = FALSE;
