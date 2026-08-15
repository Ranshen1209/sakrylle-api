-- Migration 164: Add groups.image_only discriminator.
-- Created: 2026-06-13
-- Reason: allow_image_generation became overloaded on 2026-06-13 (gotcha #5):
--         text coding groups 3 (GPT-Pro-Special) and 14 (GPT-Pro) were set
--         allow_image_generation=true purely to pass the Codex image_generation
--         tool gate. That broke the OIDC consent-page bucketing and the OAuth
--         scope access filter, which both used allow_image_generation as the
--         "is this an image group" proxy. image_only is the dedicated, narrow
--         signal ("serves only the Image API"); allow_image_generation keeps its
--         original gate meaning untouched. SQL-maintained — new image groups must
--         set image_only=true by hand + restart sub2api (group cache).

ALTER TABLE groups ADD COLUMN IF NOT EXISTS image_only boolean NOT NULL DEFAULT false;

-- Backfill the real image-only groups (GPT-Image, GPT-Image-2-4K, GPT-Image-2-Async).
UPDATE groups SET image_only = true WHERE id IN (5, 11, 21);

-- Verify.
SELECT id, name, allow_image_generation, image_only
FROM groups
WHERE allow_image_generation = true
ORDER BY id;
