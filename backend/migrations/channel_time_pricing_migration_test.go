package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelTimePricingMigration(t *testing.T) {
	content, err := FS.ReadFile("222_channel_time_pricing.sql")
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS channel_model_pricing_versions")
	require.Contains(t, sql, "pricing_id BIGINT NOT NULL REFERENCES channel_model_pricing(id) ON DELETE CASCADE")
	require.Contains(t, sql, "CREATE TABLE IF NOT EXISTS channel_pricing_time_windows")
	require.Contains(t, sql, "version_id BIGINT NOT NULL REFERENCES channel_model_pricing_versions(id) ON DELETE CASCADE")
	require.Contains(t, sql, "CHECK (start_minute < end_minute)")
	require.Contains(t, sql, "CHECK (weekdays BETWEEN 1 AND 127)")
}
