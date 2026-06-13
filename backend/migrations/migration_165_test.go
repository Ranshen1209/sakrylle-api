package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration165ReaddsEmailVerifiedIdempotently(t *testing.T) {
	content, err := FS.ReadFile("165_readd_users_email_verified.sql")
	require.NoError(t, err)
	sql := string(content)

	// Idempotent re-add so it is a no-op where the column already exists
	// (production) and repairs fresh/integration DBs that lost it to 157's
	// erroneous "+migrate Down" DROP.
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS email_verified")
	require.Contains(t, sql, "NOT NULL DEFAULT false")
	// The executable statement must be a pure re-add: no DROP in any real SQL
	// line (comments may mention DROP when explaining 157's bug, so check only
	// non-comment lines).
	for _, line := range strings.Split(sql, "\n") {
		stmt := strings.TrimSpace(line)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		require.NotContains(t, stmt, "DROP", "migration 165 must only re-add the column")
	}
}
