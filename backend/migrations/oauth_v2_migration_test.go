package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigration145OAuthV2EmbedsAllRequiredSchema asserts the OAuth v2
// migration carries every contract item from docs/OAUTH_V2_DESIGN.md §10.
// Each block here matches a specific design-doc requirement; if a future
// rebase silently drops one of these clauses we want the test to fail
// before the SQL is applied to a real database.
func TestMigration145OAuthV2EmbedsAllRequiredSchema(t *testing.T) {
	content, err := FS.ReadFile("145_oauth_v2.sql")
	require.NoError(t, err)
	sql := string(content)

	// §10.1: oauth_clients new columns and CHECK constraint.
	require.Contains(t, sql, "ALTER TABLE oauth_clients")
	for _, col := range []string{
		"client_type VARCHAR(32) NOT NULL DEFAULT 'public'",
		"app_type VARCHAR(32) NOT NULL DEFAULT 'unknown'",
		"trusted_first_party BOOLEAN NOT NULL DEFAULT FALSE",
		"default_scopes JSONB NOT NULL DEFAULT '[]'::jsonb",
		"allowed_group_ids JSONB",
		"allowed_origins JSONB NOT NULL DEFAULT '[]'::jsonb",
		"logout_redirect_uris JSONB NOT NULL DEFAULT '[]'::jsonb",
		"device_flow_enabled BOOLEAN NOT NULL DEFAULT FALSE",
		"allow_refresh_without_offline_access BOOLEAN NOT NULL DEFAULT FALSE",
		"icon_url TEXT",
		"homepage_url TEXT",
		"privacy_url TEXT",
		"terms_url TEXT",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+col, "missing oauth_clients column %q", col)
	}
	require.Contains(t, sql, "oauth_clients_allowed_group_ids_nonempty_check")
	require.Contains(t, sql, "allowed_group_ids IS NULL OR jsonb_array_length(allowed_group_ids) > 0")

	// §10.2: oauth_codes new columns.
	require.Contains(t, sql, "ALTER TABLE oauth_codes")
	for _, col := range []string{
		"group_id BIGINT",
		"grant_id VARCHAR(64)",
		"allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb",
		"device_id VARCHAR(128)",
		"device_name VARCHAR(200)",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+col, "missing oauth_codes column %q", col)
	}

	// §10.3: oauth_authorize_transactions table + indexes.
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS oauth_authorize_transactions")
	require.Contains(t, sql, "transaction_id VARCHAR(96) NOT NULL UNIQUE")
	require.Contains(t, sql, "csrf_hash VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "code_challenge_method VARCHAR(10) NOT NULL DEFAULT 'S256'")
	require.Contains(t, sql, "idx_oauth_authorize_transactions_expires_at")
	require.Contains(t, sql, "idx_oauth_authorize_transactions_client_id")

	// §10.4: oauth_refresh_tokens new columns + backfill + indexes.
	for _, col := range []string{
		"grant_id VARCHAR(64)",
		"token_family_id VARCHAR(64)",
		"group_id BIGINT",
		"allowed_groups_snapshot JSONB NOT NULL DEFAULT '[]'::jsonb",
		"device_id VARCHAR(128)",
		"device_name VARCHAR(200)",
		"last_used_at TIMESTAMPTZ",
		"reuse_detected_at TIMESTAMPTZ",
	} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+col, "missing oauth_refresh_tokens column %q", col)
	}
	require.Contains(t, sql, "UPDATE oauth_refresh_tokens rt")
	require.Contains(t, sql, "'legacy-grant-' || rt.id::text")
	require.Contains(t, sql, "'legacy-family-' || rt.id::text")
	require.Contains(t, sql, "idx_oauth_refresh_tokens_grant_id")
	require.Contains(t, sql, "idx_oauth_refresh_tokens_token_family_id")
	require.Contains(t, sql, "idx_oauth_refresh_tokens_user_client_active")
	require.Contains(t, sql, "idx_oauth_refresh_tokens_grant_active")
	require.Contains(t, sql, "idx_oauth_refresh_tokens_family_active")

	// §10.5: oauth_access_tokens table + indexes + reconciliation backfill.
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS oauth_access_tokens")
	require.Contains(t, sql, "api_key_id BIGINT NOT NULL UNIQUE")
	require.Contains(t, sql, "idx_oauth_access_tokens_user_active")
	require.Contains(t, sql, "idx_oauth_access_tokens_user_client_active")
	require.Contains(t, sql, "INSERT INTO oauth_access_tokens")
	require.Contains(t, sql, "'legacy-grant-ak-' || ak.id::text")
	require.Contains(t, sql, "'legacy-family-ak-' || ak.id::text")
	require.Contains(t, sql, "ON CONFLICT (api_key_id) DO NOTHING")
	require.Contains(t, sql, "ak.created_at + INTERVAL '24 hours'")
	require.Contains(t, sql, "ak.key LIKE 'sk_oauth_%'")
	require.Contains(t, sql, "'[\"image_generation\", \"balance:read\", \"models:read\"]'::jsonb")

	// §10.6: oauth_device_codes table + partial unique index + state CHECKs.
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS oauth_device_codes")
	require.Contains(t, sql, "device_code_hash VARCHAR(64) NOT NULL UNIQUE")
	require.Contains(t, sql, "user_code_hash VARCHAR(64) NOT NULL")
	require.Contains(t, sql, "idx_oauth_device_codes_user_code_active_unique")
	require.Contains(t, sql, "WHERE status IN ('pending', 'approved')")
	require.Contains(t, sql, "oauth_device_codes_status_consumed_check")
	require.Contains(t, sql, "(status = 'consumed') = (consumed_at IS NOT NULL)")
	require.Contains(t, sql, "oauth_device_codes_status_denied_check")
	require.Contains(t, sql, "oauth_device_codes_status_enum_check")
	require.Contains(t, sql, "status IN ('pending', 'approved', 'denied', 'consumed', 'expired')")

	// §10.7: settings seeded with ON CONFLICT DO NOTHING. Enforcement off
	// in migration; operator flips after smoke per §15.2.
	require.Contains(t, sql, "('oauth_issuer'")
	require.Contains(t, sql, "('oauth_scope_enforcement_enabled', 'false'")
	require.Contains(t, sql, "('oauth_device_flow_enabled', 'true'")
	require.Contains(t, sql, "('oauth_v2_ui_enabled', 'false'")
	require.Contains(t, sql, "ON CONFLICT (key) DO NOTHING")
	// Hard guarantee: enforcement default in migration is false, NOT true.
	// Test the surface the operator sees; if a future commit silently
	// flips it, this fails before reaching production.
	require.NotContains(t, sql, "('oauth_scope_enforcement_enabled', 'true'")

	// §10.8: oauth_cleanup_expired() function (idempotent).
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION oauth_cleanup_expired()")
	require.Contains(t, sql, "DELETE FROM oauth_authorize_transactions")
	require.Contains(t, sql, "DELETE FROM oauth_device_codes")

	// Forward-only / idempotency invariants: no DROP TABLE, no
	// non-idempotent INSERT, no ALTER without IF NOT EXISTS for new cols.
	require.NotContains(t, sql, "DROP TABLE")
	// Every ADD COLUMN we ship must carry IF NOT EXISTS — grep the
	// surface for any missing ones.
	for _, line := range strings.Split(sql, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "ADD COLUMN ") && !strings.Contains(trimmed, "IF NOT EXISTS") {
			t.Fatalf("non-idempotent ADD COLUMN in migration 145: %q", trimmed)
		}
	}
}

// TestMigration145OAuthV2DoesNotSeedSakrylleGroupIDs guards the design rule
// that the generic schema migration must not depend on Sakrylle production
// groups. Sakrylle-specific seeds (e.g. allowed_group_ids for
// sakrylle-image-playground bound to numeric group IDs 5/3/4) belong in a
// separate seed step (146 or operator SQL) so forks of sub2api don't ship
// with sakrylle's production group plumbing.
//
// The reconciliation INSERT in §10.5 deliberately uses
// 'sakrylle-image-playground' as a fallback client_id for legacy
// sk_oauth_ rows that have no associated refresh-token row — that is a
// continuity guarantee for already-issued tokens, not a fresh seed of the
// client row. We assert no NEW client row is INSERTed/UPDATEd in 145.
func TestMigration145OAuthV2DoesNotSeedSakrylleGroupIDs(t *testing.T) {
	content, err := FS.ReadFile("145_oauth_v2.sql")
	require.NoError(t, err)
	sql := string(content)

	// We do NOT mutate the sakrylle-image-playground client row in 145;
	// that row's app_type/allowed_group_ids upgrade is a Sakrylle-specific
	// follow-up (migration 146 or operator SQL).
	require.NotContains(t, sql, "UPDATE oauth_clients")
	require.NotContains(t, sql, "INSERT INTO oauth_clients")

	// Generic backfill conditions reference api_keys.group_id, not a
	// hardcoded numeric group ID. Quick sanity check: no naked
	// "group_id = 5" or similar.
	for _, hardcoded := range []string{
		"group_id = 5",
		"group_id=5",
		"group_id = 3",
		"group_id = 4",
		"default_group_id = 5",
	} {
		require.NotContains(t, sql, hardcoded, "migration 145 must not hardcode Sakrylle production group ids: %q", hardcoded)
	}
}
