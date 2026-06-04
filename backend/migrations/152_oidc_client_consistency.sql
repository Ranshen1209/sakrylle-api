-- Idempotent OIDC client registration consistency fixes.
--
-- Aligns the production oauth_clients table with the documented client
-- registration matrix (sakrylle-docs/03 §9). All statements use ON CONFLICT
-- DO NOTHING or WHERE clauses so this migration is safe to run repeatedly and
-- on databases that already have the target state (including those that ran
-- the earlier migrations 148, 149, 150, 151).
--
-- Key fixes:
--   1. Set trusted_first_party=true on all Sakrylle-owned first-party clients.
--   2. Ensure OIDC scopes (openid, profile, email) are in allowed_scopes and
--      default_scopes for each first-party client.
--   3. Disable sakrylle-image-playground-v2 (architecture doc §9 decided to
--      reuse the original sakrylle-image-playground, not create a -v2).
--   4. Add logout_redirect_uris for first-party clients that support logout.

-- ── 1. Set trusted_first_party=true on first-party clients ──────────────────

UPDATE oauth_clients SET trusted_first_party=TRUE, updated_at=NOW()
WHERE client_id IN (
    'sakrylle-image-playground',
    'sakrylle-cli',
    'sakrylle-desktop'
) AND trusted_first_party=FALSE;

-- ── 2. sakrylle-image-playground: add OIDC scopes + logout ──────────────────

-- Add openid/profile/email to allowed_scopes if not already present.
UPDATE oauth_clients SET
    allowed_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.allowed_scopes, '[]'::jsonb) ||
            '["openid","profile","email"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-image-playground'
AND NOT (allowed_scopes @> '["openid","profile","email"]'::jsonb);

-- Add default_scopes if empty.
UPDATE oauth_clients SET
    default_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.default_scopes, '[]'::jsonb) ||
            '["openid","profile","email","images:create","account:balance:read","models:read","offline_access"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-image-playground'
AND (default_scopes IS NULL OR jsonb_array_length(default_scopes) = 0);

-- Add logout_redirect_uris for Image.
UPDATE oauth_clients SET
    logout_redirect_uris = '["https://image.sakrylle.com"]'::jsonb,
    updated_at = NOW()
WHERE client_id = 'sakrylle-image-playground'
AND (logout_redirect_uris IS NULL OR jsonb_array_length(logout_redirect_uris) = 0);

-- ── 3. sakrylle-cli: add OIDC scopes ────────────────────────────────────────

UPDATE oauth_clients SET
    allowed_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.allowed_scopes, '[]'::jsonb) ||
            '["openid","profile","email"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-cli'
AND NOT (allowed_scopes @> '["openid","profile","email"]'::jsonb);

UPDATE oauth_clients SET
    default_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.default_scopes, '[]'::jsonb) ||
            '["openid","profile","email"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-cli'
AND NOT (default_scopes @> '["openid","profile","email"]'::jsonb);

-- ── 4. sakrylle-desktop: add OIDC scopes ────────────────────────────────────

UPDATE oauth_clients SET
    allowed_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.allowed_scopes, '[]'::jsonb) ||
            '["openid","profile","email"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-desktop'
AND NOT (allowed_scopes @> '["openid","profile","email"]'::jsonb);

UPDATE oauth_clients SET
    default_scopes = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.default_scopes, '[]'::jsonb) ||
            '["openid","profile","email"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-desktop'
AND NOT (default_scopes @> '["openid","profile","email"]'::jsonb);

-- ── 5. Disable sakrylle-image-playground-v2 ─────────────────────────────────
-- Architecture decision (03 §9): reuse the original sakrylle-image-playground
-- client_id, do NOT create a -v2. This migration disables the -v2 client if
-- it was created by a prior migration. It does NOT delete the row (preserving
-- any grants that may have been issued).

UPDATE oauth_clients SET disabled=TRUE, updated_at=NOW()
WHERE client_id = 'sakrylle-image-playground-v2'
AND disabled=FALSE;

-- ── 6. Ensure sakrylle-image-playground redirect_uris includes localhost ─────

UPDATE oauth_clients SET
    redirect_uris = (
        SELECT COALESCE(jsonb_agg(DISTINCT elem ORDER BY elem), '[]'::jsonb)
        FROM jsonb_array_elements_text(
            COALESCE(oauth_clients.redirect_uris, '[]'::jsonb) ||
            '["http://localhost:5173/oauth/callback"]'::jsonb
        ) AS elem
    ),
    updated_at = NOW()
WHERE client_id = 'sakrylle-image-playground'
AND NOT (redirect_uris @> '["http://localhost:5173/oauth/callback"]'::jsonb);
