-- Migration 162: Grant chat.completions:create scope to sakrylle-image-playground
-- Created: 2026-06-10
-- Reason: Persist an out-of-band psql edit so it survives a fresh DB rebuild.
--         The legacy sakrylle-image-playground client needs chat.completions:create
--         in its allowed_scopes; without this migration the scope grant is lost
--         whenever the database is recreated from migrations.

-- Append chat.completions:create to allowed_scopes if not already present.
UPDATE oauth_clients
SET allowed_scopes = allowed_scopes || '["chat.completions:create"]'::jsonb,
    updated_at = now()
WHERE client_id = 'sakrylle-image-playground'
  AND NOT (allowed_scopes ? 'chat.completions:create');

-- Verify the update
SELECT
    client_id,
    allowed_scopes
FROM oauth_clients
WHERE client_id = 'sakrylle-image-playground';
