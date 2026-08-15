-- Migration 150: Grant openid, profile, and email scopes to first-party OAuth clients
-- Created: 2026-06-04
-- Reason: Enable OIDC id_token issuance for sakrylle-cli, sakrylle-desktop, and sakrylle-image-playground

-- Add openid, profile, and email to allowed_scopes for clients that don't already have them
UPDATE oauth_clients
SET allowed_scopes = (
    SELECT jsonb_agg(DISTINCT elem)
    FROM (
        SELECT jsonb_array_elements_text(allowed_scopes) AS elem
        UNION ALL
        SELECT unnest(ARRAY['openid', 'profile', 'email'])
    ) AS combined
)
WHERE client_id IN (
    'sakrylle-cli',
    'sakrylle-desktop',
    'sakrylle-image-playground',
    'sakrylle-image-playground-v2'
)
AND NOT (
    allowed_scopes ? 'openid'
    AND allowed_scopes ? 'profile'
    AND allowed_scopes ? 'email'
);

-- Verify the update
SELECT
    client_id,
    allowed_scopes
FROM oauth_clients
WHERE client_id IN (
    'sakrylle-cli',
    'sakrylle-desktop',
    'sakrylle-image-playground',
    'sakrylle-image-playground-v2'
);
