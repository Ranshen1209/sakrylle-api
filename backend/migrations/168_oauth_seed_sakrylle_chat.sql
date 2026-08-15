-- Migration 168: Register Sakrylle Chat native OIDC client
-- Created: 2026-06-15
-- Reason: Sakrylle Chat uses Authorization Code + PKCE against the production
--         OIDC issuer. Persist the production client registration so fresh DB
--         rebuilds do not regress to oauth client not found.

INSERT INTO oauth_clients (
    client_id,
    name,
    client_type,
    app_type,
    client_secret_hash,
    redirect_uris,
    allowed_scopes,
    default_scopes,
    allowed_origins,
    pkce_required,
    device_flow_enabled,
    allow_refresh_without_offline_access,
    default_group_id,
    access_token_ttl_seconds,
    refresh_token_ttl_seconds,
    disabled,
    trusted_first_party,
    signing_algorithm,
    subject_type
) VALUES (
    'sakrylle-chat',
    'Sakrylle Chat',
    'public',
    'chat',
    NULL,
    '["sakrylle-chat://oauth/callback", "http://127.0.0.1/callback"]'::jsonb,
    '["openid", "profile", "email", "models:read", "chat.completions:create", "account:balance:read", "offline_access"]'::jsonb,
    '["openid", "profile", "email", "models:read", "chat.completions:create", "account:balance:read", "offline_access"]'::jsonb,
    '[]'::jsonb,
    TRUE,
    FALSE,
    FALSE,
    NULL,
    86400,
    2592000,
    FALSE,
    TRUE,
    'RS256',
    'public'
)
ON CONFLICT (client_id) DO UPDATE SET
    name                                 = EXCLUDED.name,
    client_type                          = EXCLUDED.client_type,
    app_type                             = EXCLUDED.app_type,
    client_secret_hash                   = NULL,
    redirect_uris                        = EXCLUDED.redirect_uris,
    allowed_scopes                       = EXCLUDED.allowed_scopes,
    default_scopes                       = EXCLUDED.default_scopes,
    allowed_origins                      = EXCLUDED.allowed_origins,
    pkce_required                        = TRUE,
    device_flow_enabled                  = FALSE,
    allow_refresh_without_offline_access = FALSE,
    disabled                             = FALSE,
    trusted_first_party                  = TRUE,
    signing_algorithm                    = EXCLUDED.signing_algorithm,
    subject_type                         = EXCLUDED.subject_type,
    updated_at                           = NOW();

SELECT
    client_id,
    client_type,
    app_type,
    redirect_uris,
    allowed_scopes,
    pkce_required,
    disabled,
    client_secret_hash IS NOT NULL AS has_secret
FROM oauth_clients
WHERE client_id = 'sakrylle-chat';
