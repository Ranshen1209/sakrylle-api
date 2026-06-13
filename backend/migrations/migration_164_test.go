package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration164AddsImageOnlyColumnIdempotently(t *testing.T) {
	content, err := FS.ReadFile("164_group_image_only.sql")
	require.NoError(t, err)
	sql := string(content)

	// Idempotent add so a re-run / partially-migrated DB is safe.
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS image_only")
	require.Contains(t, sql, "NOT NULL DEFAULT false")
	// Backfill exactly the three real image groups.
	require.Contains(t, sql, "UPDATE groups SET image_only = true")
	require.Contains(t, sql, "WHERE id IN (5, 11, 21)")
}
