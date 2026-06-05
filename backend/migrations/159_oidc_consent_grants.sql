-- OIDC consent grants for third-party client authorization tracking.
-- Records user consent so third-party clients can auto-approve on
-- subsequent requests without re-showing the consent page.

CREATE TABLE IF NOT EXISTS oauth_grants (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id TEXT NOT NULL,
    scope TEXT[] NOT NULL DEFAULT '{}',
    granted_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMP WITH TIME ZONE,
    UNIQUE(user_id, client_id)
);

CREATE INDEX IF NOT EXISTS idx_oauth_grants_user_client ON oauth_grants(user_id, client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_grants_expires ON oauth_grants(expires_at) WHERE expires_at IS NOT NULL;
