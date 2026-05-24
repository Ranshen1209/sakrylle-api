-- Sakrylle-specific OAuth provider seed.
--
-- Forks of sub2api should NOT run this migration as-is — it binds the OAuth
-- provider to group_id=5 (GPT-Image in sakrylle's DB) and registers the
-- sakrylle-image-playground public client. Either delete this file or replace
-- it with your own seed before deploying to a non-sakrylle environment.

INSERT INTO settings (key, value, updated_at) VALUES
    ('oauth_default_group_id', '5', NOW())
ON CONFLICT (key) DO NOTHING;

INSERT INTO oauth_clients (
    client_id,
    name,
    redirect_uris,
    allowed_scopes,
    pkce_required,
    default_group_id,
    access_token_ttl_seconds,
    refresh_token_ttl_seconds
) VALUES (
    'sakrylle-image-playground',
    'Sakrylle Image Playground',
    '["https://image.sakrylle.com/oauth/callback", "http://localhost:5173/oauth/callback"]'::jsonb,
    '["image_generation", "balance:read", "models:read"]'::jsonb,
    TRUE,
    5,
    86400,
    2592000
) ON CONFLICT (client_id) DO NOTHING;
