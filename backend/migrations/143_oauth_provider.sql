-- OAuth 2.0 provider tables (Authorization Code + PKCE).
--
-- Issued access_tokens live on the existing `api_keys` table with a
-- `sk_oauth_` prefix and `expires_at` set, so /v1/* middleware, billing,
-- rate limiting, and Redis cache invalidation work unchanged.

CREATE TABLE IF NOT EXISTS oauth_clients (
    id BIGSERIAL PRIMARY KEY,
    client_id VARCHAR(128) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    client_secret_hash TEXT,
    redirect_uris JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    pkce_required BOOLEAN NOT NULL DEFAULT TRUE,
    default_group_id BIGINT,
    access_token_ttl_seconds INT NOT NULL DEFAULT 86400,
    refresh_token_ttl_seconds INT NOT NULL DEFAULT 2592000,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_oauth_clients_disabled ON oauth_clients(disabled);

CREATE TABLE IF NOT EXISTS oauth_codes (
    id BIGSERIAL PRIMARY KEY,
    code_hash VARCHAR(64) NOT NULL UNIQUE,
    client_id VARCHAR(128) NOT NULL,
    user_id BIGINT NOT NULL,
    redirect_uri TEXT NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    code_challenge VARCHAR(128) NOT NULL,
    code_challenge_method VARCHAR(10) NOT NULL DEFAULT 'S256',
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_oauth_codes_expires_at ON oauth_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_codes_user_id ON oauth_codes(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_codes_client_id ON oauth_codes(client_id);

CREATE TABLE IF NOT EXISTS oauth_refresh_tokens (
    id BIGSERIAL PRIMARY KEY,
    token_hash VARCHAR(64) NOT NULL UNIQUE,
    client_id VARCHAR(128) NOT NULL,
    user_id BIGINT NOT NULL,
    api_key_id BIGINT NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    rotated_to_hash VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_expires_at ON oauth_refresh_tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_user_id ON oauth_refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_client_id ON oauth_refresh_tokens(client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_api_key_id ON oauth_refresh_tokens(api_key_id);

-- Feature toggle. Default group binding is operator-specific; configure it
-- via the oauth_default_group_id setting or per-client default_group_id.
INSERT INTO settings (key, value, updated_at) VALUES
    ('oauth_provider_enabled', 'true', NOW())
ON CONFLICT (key) DO NOTHING;
