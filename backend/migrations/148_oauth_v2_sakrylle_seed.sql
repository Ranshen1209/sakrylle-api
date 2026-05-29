-- Sakrylle-specific OAuth v2 client seeds.
--
-- Forks of sub2api should NOT run this migration as-is — it registers
-- Sakrylle-specific first-party clients. Either delete this file or replace
-- it with your own seed before deploying to a non-sakrylle environment.
--
-- All INSERTs use ON CONFLICT (client_id) DO NOTHING for idempotency.
-- default_group_id is NULL throughout — operators set this per deployment
-- via the admin UI or direct SQL, avoiding hard-coded production group IDs.

-- ── 1. Upgrade legacy sakrylle-image-playground to v2 schema ───────────────

UPDATE oauth_clients SET
    app_type                        = 'image',
    allow_refresh_without_offline_access = TRUE,
    allowed_scopes                  = (
        -- Merge legacy scopes with canonical v2 scopes, deduplicating.
        SELECT jsonb_agg(DISTINCT elem ORDER BY elem)
        FROM jsonb_array_elements_text(
            '["image_generation","balance:read","models:read","account:balance:read","models:read","images:create","responses:create","offline_access"]'::jsonb
        ) AS elem
    ),
    updated_at                      = NOW()
WHERE client_id = 'sakrylle-image-playground';

-- ── 2. sakrylle-cli ─────────────────────────────────────────────────────────
-- Public CLI client. Device flow enabled so headless / SSH environments can
-- authenticate without a browser on the same machine.

INSERT INTO oauth_clients (
    client_id,
    name,
    client_type,
    app_type,
    redirect_uris,
    allowed_scopes,
    default_scopes,
    allowed_origins,
    pkce_required,
    device_flow_enabled,
    default_group_id,
    access_token_ttl_seconds,
    refresh_token_ttl_seconds
) VALUES (
    'sakrylle-cli',
    'Sakrylle CLI',
    'public',
    'cli',
    '[]'::jsonb,
    '["profile:read","account:read","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb,
    '["profile:read","account:read","models:read","responses:create","messages:create","usage:read","offline_access"]'::jsonb,
    '[]'::jsonb,
    TRUE,
    TRUE,
    NULL,  -- operator sets per deployment (e.g. GPT-Pro group id)
    86400,
    2592000
) ON CONFLICT (client_id) DO NOTHING;

-- ── 3. sakrylle-desktop ─────────────────────────────────────────────────────
-- Public desktop client. Loopback redirect URIs cover both IPv4 and IPv6.

INSERT INTO oauth_clients (
    client_id,
    name,
    client_type,
    app_type,
    redirect_uris,
    allowed_scopes,
    default_scopes,
    allowed_origins,
    pkce_required,
    device_flow_enabled,
    default_group_id,
    access_token_ttl_seconds,
    refresh_token_ttl_seconds
) VALUES (
    'sakrylle-desktop',
    'Sakrylle Desktop',
    'public',
    'desktop',
    '["http://127.0.0.1/oauth/callback","http://[::1]/oauth/callback","http://localhost/oauth/callback"]'::jsonb,
    '["profile:read","account:read","models:read","chat.completions:create","responses:create","messages:create","usage:read","offline_access"]'::jsonb,
    '["profile:read","account:read","models:read","chat.completions:create","responses:create","messages:create","usage:read","offline_access"]'::jsonb,
    '[]'::jsonb,
    TRUE,
    FALSE,
    NULL,  -- operator sets per deployment
    86400,
    2592000
) ON CONFLICT (client_id) DO NOTHING;

-- ── 4. sakrylle-image-playground-v2 ─────────────────────────────────────────
-- Public web client for the Image Playground (v2). Supersedes the legacy
-- sakrylle-image-playground entry; both can coexist during the transition.

INSERT INTO oauth_clients (
    client_id,
    name,
    client_type,
    app_type,
    redirect_uris,
    allowed_scopes,
    default_scopes,
    allowed_origins,
    pkce_required,
    device_flow_enabled,
    default_group_id,
    access_token_ttl_seconds,
    refresh_token_ttl_seconds
) VALUES (
    'sakrylle-image-playground-v2',
    'Sakrylle Image Playground',
    'public',
    'image',
    '["https://image.sakrylle.com/oauth/callback"]'::jsonb,
    '["account:balance:read","models:read","images:create","responses:create","offline_access"]'::jsonb,
    '["account:balance:read","models:read","images:create","responses:create","offline_access"]'::jsonb,
    '["https://image.sakrylle.com"]'::jsonb,
    TRUE,
    FALSE,
    NULL,
    3600,
    2592000
) ON CONFLICT (client_id) DO NOTHING;

-- ── 5. oauth_issuer canonical setting ──────────────────────────────────────
-- Issue 21: operators must set oauth_issuer so RFC 8414 discovery and device
-- verification_uri use a stable URL instead of falling back to request
-- scheme://Host. Seeded here with the Sakrylle production origin; forks
-- should replace this value before deploying.
--
-- ON CONFLICT DO NOTHING: if the operator has already set a custom value via
-- the admin UI or direct SQL, this migration leaves it untouched.

INSERT INTO settings (key, value, updated_at)
VALUES ('oauth_issuer', 'https://sub.sakrylle.com', NOW())
ON CONFLICT (key) DO NOTHING;

