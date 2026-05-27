-- OAuth 2.0 v2: scope normalization, group resolution, device flow, and
-- per-device authorized-app management.
--
-- Forward-only migration. All ALTER/CREATE statements are idempotent.
-- See docs/OAUTH_V2_DESIGN.md §10 for the data model contract; field-level
-- explanations live there. This file ships generic schema only — Sakrylle
-- production seed (e.g. sakrylle-image-playground app_type/allowed_groups)
-- belongs in a separate seed step that resolves group ids by name.

-- ── 10.1: Extend oauth_clients ─────────────────────────────────────────────

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS client_type VARCHAR(32) NOT NULL DEFAULT 'public',
    ADD COLUMN IF NOT EXISTS app_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    ADD COLUMN IF NOT EXISTS trusted_first_party BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS default_scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS allowed_group_ids JSONB,
    ADD COLUMN IF NOT EXISTS allowed_origins JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS logout_redirect_uris JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_flow_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS allow_refresh_without_offline_access BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS icon_url TEXT,
    ADD COLUMN IF NOT EXISTS homepage_url TEXT,
    ADD COLUMN IF NOT EXISTS privacy_url TEXT,
    ADD COLUMN IF NOT EXISTS terms_url TEXT;

-- allowed_group_ids: NULL means "no client-level restriction" (resolved
-- against user-level rules). When non-NULL it MUST be a non-empty JSON array;
-- empty arrays look intentional but make every grant fail.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_clients_allowed_group_ids_nonempty_check'
    ) THEN
        ALTER TABLE oauth_clients
            ADD CONSTRAINT oauth_clients_allowed_group_ids_nonempty_check
            CHECK (allowed_group_ids IS NULL OR jsonb_array_length(allowed_group_ids) > 0);
    END IF;
END$$;

-- ── 10.2: Extend oauth_codes ───────────────────────────────────────────────

ALTER TABLE oauth_codes
    ADD COLUMN IF NOT EXISTS group_id BIGINT,
    ADD COLUMN IF NOT EXISTS grant_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(200);

CREATE INDEX IF NOT EXISTS idx_oauth_codes_grant_id ON oauth_codes(grant_id);

-- ── 10.3: oauth_authorize_transactions ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS oauth_authorize_transactions (
    id BIGSERIAL PRIMARY KEY,
    transaction_id VARCHAR(96) NOT NULL UNIQUE,
    csrf_hash VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    redirect_uri TEXT NOT NULL,
    response_type VARCHAR(32) NOT NULL DEFAULT 'code',
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    state TEXT NOT NULL,
    code_challenge VARCHAR(128) NOT NULL,
    code_challenge_method VARCHAR(10) NOT NULL DEFAULT 'S256',
    requested_group_id BIGINT,
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    consumed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_ip VARCHAR(64),
    created_user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_authorize_transactions_expires_at
    ON oauth_authorize_transactions(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_authorize_transactions_client_id
    ON oauth_authorize_transactions(client_id);

-- ── 10.4: Extend oauth_refresh_tokens ──────────────────────────────────────

ALTER TABLE oauth_refresh_tokens
    ADD COLUMN IF NOT EXISTS grant_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS token_family_id VARCHAR(64),
    ADD COLUMN IF NOT EXISTS group_id BIGINT,
    ADD COLUMN IF NOT EXISTS allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS device_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS device_name VARCHAR(200),
    ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reuse_detected_at TIMESTAMPTZ;

-- Backfill v2 metadata for existing legacy refresh rows. Idempotent: the
-- predicate skips rows that already have all four new identity columns set.
UPDATE oauth_refresh_tokens rt
SET
    grant_id = COALESCE(rt.grant_id, 'legacy-grant-' || rt.id::text),
    token_family_id = COALESCE(rt.token_family_id, 'legacy-family-' || rt.id::text),
    group_id = COALESCE(rt.group_id, ak.group_id),
    allowed_groups_snapshot = CASE
        WHEN rt.allowed_groups_snapshot IS NULL OR rt.allowed_groups_snapshot = '[]'::jsonb
            THEN jsonb_build_array(ak.group_id)
        ELSE rt.allowed_groups_snapshot
    END,
    device_name = COALESCE(rt.device_name, 'Legacy session')
FROM api_keys ak
WHERE rt.api_key_id = ak.id
  AND (rt.grant_id IS NULL
       OR rt.token_family_id IS NULL
       OR rt.group_id IS NULL
       OR rt.device_name IS NULL);

CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_grant_id
    ON oauth_refresh_tokens(grant_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_token_family_id
    ON oauth_refresh_tokens(token_family_id);
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_user_client_active
    ON oauth_refresh_tokens(user_id, client_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_grant_active
    ON oauth_refresh_tokens(grant_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_refresh_tokens_family_active
    ON oauth_refresh_tokens(token_family_id, expires_at)
    WHERE revoked_at IS NULL;

-- ── 10.5: oauth_access_tokens ──────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS oauth_access_tokens (
    id BIGSERIAL PRIMARY KEY,
    api_key_id BIGINT NOT NULL UNIQUE,
    grant_id VARCHAR(64) NOT NULL,
    token_family_id VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    user_id BIGINT NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    group_id BIGINT NOT NULL,
    allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb,
    app_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    last_used_ip VARCHAR(64),
    last_used_user_agent TEXT,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_client_id ON oauth_access_tokens(client_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_id ON oauth_access_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_grant_id ON oauth_access_tokens(grant_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_token_family_id ON oauth_access_tokens(token_family_id);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_expires_at ON oauth_access_tokens(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_revoked_at ON oauth_access_tokens(revoked_at);
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_active
    ON oauth_access_tokens(user_id, expires_at)
    WHERE revoked_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_oauth_access_tokens_user_client_active
    ON oauth_access_tokens(user_id, client_id, expires_at)
    WHERE revoked_at IS NULL;

-- Reconciliation backfill from api_keys for every sk_oauth_ row.
--
-- The reconciliation source of truth is api_keys (not oauth_refresh_tokens):
-- partial-failure or rollback windows could have created an access key
-- before refresh metadata was inserted. Refresh metadata is preferred when
-- present; otherwise we synthesise conservative legacy metadata and rely on
-- the operator-run report (cmd/oauth-reconcile --report-only) to flag rows
-- that lack a reliable expiry source before flipping enforcement on.
--
-- Idempotent via UNIQUE(api_key_id) + ON CONFLICT DO NOTHING.
INSERT INTO oauth_access_tokens (
    api_key_id,
    grant_id,
    token_family_id,
    client_id,
    user_id,
    scopes,
    group_id,
    allowed_groups_snapshot,
    app_type,
    device_id,
    device_name,
    issued_at,
    expires_at,
    revoked_at,
    created_at,
    updated_at
)
SELECT
    ak.id,
    COALESCE(rt.grant_id, 'legacy-grant-ak-' || ak.id::text),
    COALESCE(rt.token_family_id, 'legacy-family-ak-' || ak.id::text),
    COALESCE(rt.client_id, 'sakrylle-image-playground'),
    ak.user_id,
    CASE
        WHEN rt.scopes IS NULL OR rt.scopes = '[]'::jsonb
            THEN '["image_generation", "balance:read", "models:read"]'::jsonb
        ELSE rt.scopes
    END,
    COALESCE(rt.group_id, ak.group_id),
    CASE
        WHEN rt.allowed_groups_snapshot IS NULL OR rt.allowed_groups_snapshot = '[]'::jsonb
            THEN jsonb_build_array(ak.group_id)
        ELSE rt.allowed_groups_snapshot
    END,
    COALESCE(oc.app_type, 'unknown'),
    rt.device_id,
    COALESCE(rt.device_name, 'Legacy session'),
    ak.created_at,
    -- Conservative legacy expiry chain. Operator MUST run
    -- cmd/oauth-reconcile --report-only and disable rows with no reliable
    -- expiry source before flipping oauth_scope_enforcement_enabled=true;
    -- the 24h grace is auditable, not a silent fallback.
    COALESCE(ak.expires_at, rt.expires_at, ak.created_at + INTERVAL '24 hours'),
    CASE WHEN ak.status <> 'active' THEN NOW() ELSE NULL END,
    NOW(),
    NOW()
FROM api_keys ak
LEFT JOIN LATERAL (
    SELECT rt.*
    FROM oauth_refresh_tokens rt
    WHERE rt.api_key_id = ak.id
    ORDER BY rt.revoked_at NULLS FIRST, rt.updated_at DESC, rt.expires_at DESC, rt.id DESC
    LIMIT 1
) rt ON TRUE
LEFT JOIN oauth_clients oc ON oc.client_id = rt.client_id
WHERE ak.key LIKE 'sk_oauth_%'
  AND ak.group_id IS NOT NULL
ON CONFLICT (api_key_id) DO NOTHING;

-- ── 10.6: oauth_device_codes ───────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS oauth_device_codes (
    id BIGSERIAL PRIMARY KEY,
    device_code_hash VARCHAR(64) NOT NULL UNIQUE,
    user_code_hash VARCHAR(64) NOT NULL,
    client_id VARCHAR(128) NOT NULL,
    scopes JSONB NOT NULL DEFAULT '[]'::jsonb,
    grant_id VARCHAR(64),
    group_id BIGINT,
    device_id VARCHAR(128),
    device_name VARCHAR(200),
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    interval_seconds INT NOT NULL DEFAULT 5,
    poll_count INT NOT NULL DEFAULT 0,
    last_poll_at TIMESTAMPTZ,
    slow_down_count INT NOT NULL DEFAULT 0,
    failed_user_code_attempts INT NOT NULL DEFAULT 0,
    approved_by_user_id BIGINT,
    approved_at TIMESTAMPTZ,
    denied_at TIMESTAMPTZ,
    consumed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ NOT NULL,
    created_ip VARCHAR(64),
    created_user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_client_id ON oauth_device_codes(client_id);
-- Only one live (pending or approved) code may exist per user_code_hash; once
-- consumed/denied/expired the hash is free again.
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_device_codes_user_code_active_unique
    ON oauth_device_codes(user_code_hash)
    WHERE status IN ('pending', 'approved');
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_user_code_hash ON oauth_device_codes(user_code_hash);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_expires_at ON oauth_device_codes(expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_approved_by_user_id ON oauth_device_codes(approved_by_user_id);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_status_expires_at ON oauth_device_codes(status, expires_at);
CREATE INDEX IF NOT EXISTS idx_oauth_device_codes_grant_id ON oauth_device_codes(grant_id);

-- State / timestamp coherence: the row-state column and the timestamp column
-- must agree. Wrapped in DO blocks because PostgreSQL has no
-- "ADD CONSTRAINT IF NOT EXISTS" syntax.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_device_codes_status_consumed_check'
    ) THEN
        ALTER TABLE oauth_device_codes
            ADD CONSTRAINT oauth_device_codes_status_consumed_check
            CHECK ((status = 'consumed') = (consumed_at IS NOT NULL));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_device_codes_status_approved_check'
    ) THEN
        ALTER TABLE oauth_device_codes
            ADD CONSTRAINT oauth_device_codes_status_approved_check
            CHECK (
                (status IN ('approved', 'consumed') AND approved_at IS NOT NULL)
                OR (status NOT IN ('approved', 'consumed') AND approved_at IS NULL)
            );
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_device_codes_status_denied_check'
    ) THEN
        ALTER TABLE oauth_device_codes
            ADD CONSTRAINT oauth_device_codes_status_denied_check
            CHECK ((status = 'denied') = (denied_at IS NOT NULL));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_device_codes_status_enum_check'
    ) THEN
        ALTER TABLE oauth_device_codes
            ADD CONSTRAINT oauth_device_codes_status_enum_check
            CHECK (status IN ('pending', 'approved', 'denied', 'consumed', 'expired'));
    END IF;
END$$;

-- ── 10.7: Settings seeds (operator flips production values) ────────────────

INSERT INTO settings (key, value, updated_at) VALUES
    ('oauth_issuer', 'https://sub.sakrylle.com', NOW()),
    -- Default OFF in migration; operator flips to true after smoke per §15.2.
    ('oauth_scope_enforcement_enabled', 'false', NOW()),
    ('oauth_device_flow_enabled', 'true', NOW()),
    -- Default OFF during backend rollout; operator flips after frontend smoke.
    ('oauth_v2_ui_enabled', 'false', NOW())
ON CONFLICT (key) DO NOTHING;

-- ── 10.8: Cleanup function ─────────────────────────────────────────────────
--
-- Idempotent. Deletes expired/consumed authorize transactions and expired
-- terminal device codes after a short retention window. Does NOT hard-delete
-- access/refresh tokens — those rows are needed for authorized-app history
-- and post-incident audit; their pruning is governed by an explicit
-- retention policy elsewhere.

CREATE OR REPLACE FUNCTION oauth_cleanup_expired() RETURNS TABLE (
    transactions_deleted BIGINT,
    device_codes_deleted BIGINT
) AS $$
DECLARE
    tx_count BIGINT;
    dc_count BIGINT;
BEGIN
    DELETE FROM oauth_authorize_transactions
    WHERE expires_at < NOW() - INTERVAL '1 hour'
       OR (consumed_at IS NOT NULL AND consumed_at < NOW() - INTERVAL '1 hour');
    GET DIAGNOSTICS tx_count = ROW_COUNT;

    DELETE FROM oauth_device_codes
    WHERE status IN ('denied', 'consumed', 'expired')
      AND expires_at < NOW() - INTERVAL '24 hours';
    GET DIAGNOSTICS dc_count = ROW_COUNT;

    transactions_deleted := tx_count;
    device_codes_deleted := dc_count;
    RETURN NEXT;
END;
$$ LANGUAGE plpgsql;
