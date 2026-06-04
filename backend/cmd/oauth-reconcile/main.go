// Command oauth-reconcile reports and (optionally) backfills v2 OAuth
// access-token metadata for legacy sk_oauth_ api_keys rows.
//
//	oauth-reconcile --report-only   # default; non-zero exit if unsafe
//	oauth-reconcile --apply         # idempotent backfill, then report
//
// See docs/OAUTH_V2_DESIGN.md §10.5 / §10.9. The --apply backfill is the
// same INSERT shipped inside migration 145; running this CLI twice is a
// no-op (UNIQUE(api_key_id) + ON CONFLICT DO NOTHING).
//
// Operators MUST run --report-only and resolve the reported rows to zero
// before flipping oauth_scope_enforcement_enabled=true. The CLI exits
// non-zero whenever active sk_oauth_ rows remain that would be unsafe to
// enforce against (no access metadata, or no reliable expiry source).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
)

const (
	// legacyExpiryGraceHours is the conservative grace window written by
	// the --apply backfill when neither api_keys.expires_at nor any
	// associated refresh-token expiry is present. Mirrors migration 145.
	legacyExpiryGraceHours = 24

	// legacyDefaultClientID is the fallback client_id used when a legacy
	// row has no associated refresh-token row. Mirrors migration 145.
	legacyDefaultClientID = "sakrylle-image-playground"

	// legacyDefaultScopes is the canonical scope bundle assigned to rows that
	// have no stored scopes. Uses canonical v2 identifiers (see
	// internal/service/oauth_scopes.go: ScopeImagesCreate /
	// ScopeAccountBalanceRead / ScopeModelsRead) so post-apply rows do NOT
	// re-trigger fallbackScopesQuery's legacy "image_generation" detector.
	// NormalizeScopes still rewrites legacy aliases on existing rows, but new
	// inserts now skip the legacy step entirely.
	legacyDefaultScopes = `["images:create", "account:balance:read", "models:read"]`

	// reportSampleLimit caps how many redacted IDs we print per category.
	reportSampleLimit = 10
)

// reconcileReport summarises the state of legacy sk_oauth_ rows.
type reconcileReport struct {
	ActiveSKOAuthCount       int64
	MissingAccessMetadata    int64
	MissingExpirySource      int64
	LegacyFallbackScopes     int64
	WouldBeDisabled          int64
	SampleMissingMetadataIDs []int64
	SampleMissingExpiryIDs   []int64
	SampleFallbackScopeIDs   []int64
	SampleWouldDisableIDs    []int64
}

func main() {
	var (
		reportOnly = flag.Bool("report-only", false, "Report unsafe rows and exit non-zero if any are found. Default mode when --apply is absent.")
		apply      = flag.Bool("apply", false, "Run the idempotent oauth_access_tokens backfill, then print the report.")
	)
	flag.Parse()

	if *reportOnly && *apply {
		log.Fatalf("--report-only and --apply are mutually exclusive")
	}
	if !*reportOnly && !*apply {
		// Default to report-only: safe, idempotent, never writes.
		*reportOnly = true
	}

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	client, sqlDB, err := repository.InitEnt(cfg)
	if err != nil {
		log.Fatalf("failed to init db: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("failed to close ent client: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if *apply {
		fmt.Println("oauth-reconcile: --apply mode")
		affected, err := applyBackfill(ctx, sqlDB)
		if err != nil {
			log.Fatalf("backfill failed: %v", err)
		}
		fmt.Printf("backfill inserted %d new oauth_access_tokens row(s) (existing rows untouched).\n", affected)

		// Summary audit event — one JSON line for easy log aggregation.
		summary := map[string]any{
			"event":         "oauth_reconcile_apply_summary",
			"rows_inserted": affected,
			"timestamp":     time.Now().UTC().Format(time.RFC3339),
		}
		if b, err := json.Marshal(summary); err == nil {
			slog.Info("oauth_reconcile_audit", "action", "apply_summary", "summary", string(b))
		}
	} else {
		fmt.Println("oauth-reconcile: --report-only mode (no writes)")
	}

	rep, err := buildReport(ctx, sqlDB)
	if err != nil {
		log.Fatalf("report failed: %v", err)
	}
	printReport(rep)

	// Exit non-zero whenever enforcement would be unsafe. After --apply,
	// the only remaining unsafe class is rows with no expiry source the
	// operator has not explicitly accepted via apply. The CLI is
	// intentionally strict; operators flip to zero before turning on
	// oauth_scope_enforcement_enabled.
	if rep.MissingAccessMetadata > 0 || rep.MissingExpirySource > 0 {
		fmt.Fprintln(os.Stderr, "unsafe: enforcement is not yet safe to enable; resolve reported rows.")
		os.Exit(1)
	}
}

// applyBackfill performs the idempotent oauth_access_tokens INSERT defined
// in migration 145 / §10.5. UNIQUE(api_key_id) makes the second run a
// no-op except for report timestamps.
//
// The SQL is intentionally identical in shape to the migration so the CLI
// stays a verbatim re-run path: rolling back v2 then re-deploying must be
// equivalent to the migration's first pass.
//
// RETURNING lets us emit one structured audit event per inserted row (Issue 19).
func applyBackfill(ctx context.Context, db *sql.DB) (int64, error) {
	rows, err := db.QueryContext(ctx, `
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
    COALESCE(rt.client_id, $1),
    ak.user_id,
    CASE
        WHEN rt.scopes IS NULL OR rt.scopes = '[]'::jsonb
            THEN $2::jsonb
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
    COALESCE(ak.expires_at, rt.expires_at, ak.created_at + ($3::text || ' hours')::interval),
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
ON CONFLICT (api_key_id) DO NOTHING
RETURNING api_key_id, grant_id, token_family_id, client_id, scopes::text
`,
		legacyDefaultClientID,
		legacyDefaultScopes,
		fmt.Sprintf("%d", legacyExpiryGraceHours),
	)
	if err != nil {
		return 0, fmt.Errorf("oauth_access_tokens backfill: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var n int64
	for rows.Next() {
		var apiKeyID int64
		var grantID, familyID, clientID, scopesJSON string
		if err := rows.Scan(&apiKeyID, &grantID, &familyID, &clientID, &scopesJSON); err != nil {
			return n, fmt.Errorf("scan backfill row: %w", err)
		}
		n++

		usedLegacyGrant := grantID == fmt.Sprintf("legacy-grant-ak-%d", apiKeyID)
		usedLegacyFamily := familyID == fmt.Sprintf("legacy-family-ak-%d", apiKeyID)
		usedFallbackClient := clientID == legacyDefaultClientID
		usedFallbackScopes := scopesJSON == legacyDefaultScopes

		slog.Info("oauth_reconcile_audit",
			"action", "backfill_access_metadata",
			"api_key_id", apiKeyID,
			"grant_id", grantID,
			"token_family_id", familyID,
			"client_id", clientID,
			"fallback_scopes", usedFallbackScopes,
			"fallback_grant_id", usedLegacyGrant,
			"fallback_family_id", usedLegacyFamily,
			"fallback_client_id", usedFallbackClient,
		)
	}
	if err := rows.Err(); err != nil {
		return n, fmt.Errorf("iterate backfill rows: %w", err)
	}
	return n, nil
}

// buildReport reads counts and a few sample IDs for each unsafe category.
// All counts are over `api_keys.key LIKE 'sk_oauth_%' AND status='active'`
// joined against the v2 metadata tables, so post-backfill the unsafe
// counts should drop to zero for any operator who first ran --apply.
func buildReport(ctx context.Context, db *sql.DB) (*reconcileReport, error) {
	rep := &reconcileReport{}

	if err := db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM api_keys
WHERE key LIKE 'sk_oauth_%' AND status='active'`).Scan(&rep.ActiveSKOAuthCount); err != nil {
		return nil, fmt.Errorf("count active sk_oauth_: %w", err)
	}

	missingMetadataQuery := `
SELECT ak.id FROM api_keys ak
LEFT JOIN oauth_access_tokens t ON t.api_key_id = ak.id
WHERE ak.key LIKE 'sk_oauth_%' AND ak.status='active'
  AND t.id IS NULL`
	if err := scanCountAndSample(ctx, db, missingMetadataQuery, &rep.MissingAccessMetadata, &rep.SampleMissingMetadataIDs); err != nil {
		return nil, fmt.Errorf("missing access metadata: %w", err)
	}

	missingExpiryQuery := `
SELECT ak.id FROM api_keys ak
LEFT JOIN LATERAL (
    SELECT rt.*
    FROM oauth_refresh_tokens rt
    WHERE rt.api_key_id = ak.id
    ORDER BY rt.revoked_at NULLS FIRST, rt.updated_at DESC, rt.expires_at DESC, rt.id DESC
    LIMIT 1
) rt ON TRUE
WHERE ak.key LIKE 'sk_oauth_%' AND ak.status='active'
  AND ak.expires_at IS NULL
  AND rt.expires_at IS NULL`
	if err := scanCountAndSample(ctx, db, missingExpiryQuery, &rep.MissingExpirySource, &rep.SampleMissingExpiryIDs); err != nil {
		return nil, fmt.Errorf("missing expiry source: %w", err)
	}

	fallbackScopesQuery := `
SELECT ak.id FROM api_keys ak
JOIN oauth_access_tokens t ON t.api_key_id = ak.id
WHERE ak.key LIKE 'sk_oauth_%' AND ak.status='active'
  AND t.scopes @> '["image_generation"]'::jsonb`
	if err := scanCountAndSample(ctx, db, fallbackScopesQuery, &rep.LegacyFallbackScopes, &rep.SampleFallbackScopeIDs); err != nil {
		return nil, fmt.Errorf("legacy fallback scopes: %w", err)
	}

	wouldDisableQuery := `
SELECT ak.id FROM api_keys ak
LEFT JOIN oauth_access_tokens t ON t.api_key_id = ak.id
WHERE ak.key LIKE 'sk_oauth_%'
  AND ak.status='active'
  AND ak.group_id IS NULL`
	if err := scanCountAndSample(ctx, db, wouldDisableQuery, &rep.WouldBeDisabled, &rep.SampleWouldDisableIDs); err != nil {
		return nil, fmt.Errorf("would-be-disabled rows: %w", err)
	}

	return rep, nil
}

// scanCountAndSample runs the predicate query and fills count + a small
// redacted sample of api_key IDs.
func scanCountAndSample(ctx context.Context, db *sql.DB, query string, count *int64, sample *[]int64) error {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		*count++
		if int64(len(*sample)) < reportSampleLimit {
			*sample = append(*sample, id)
		}
	}
	return rows.Err()
}

func printReport(rep *reconcileReport) {
	fmt.Println()
	fmt.Println("── oauth-reconcile report ──────────────────────────────────")
	fmt.Printf("active sk_oauth_ api_keys rows           : %d\n", rep.ActiveSKOAuthCount)
	fmt.Printf("  missing oauth_access_tokens metadata   : %d  %s\n", rep.MissingAccessMetadata, sampleSuffix(rep.SampleMissingMetadataIDs))
	fmt.Printf("  no reliable expiry source              : %d  %s\n", rep.MissingExpirySource, sampleSuffix(rep.SampleMissingExpiryIDs))
	fmt.Printf("  using legacy 'image_generation' scope  : %d  %s\n", rep.LegacyFallbackScopes, sampleSuffix(rep.SampleFallbackScopeIDs))
	fmt.Printf("  would be disabled (no group binding)   : %d  %s\n", rep.WouldBeDisabled, sampleSuffix(rep.SampleWouldDisableIDs))
	fmt.Println("─────────────────────────────────────────────────────────────")
	if rep.MissingAccessMetadata == 0 && rep.MissingExpirySource == 0 {
		fmt.Println("OK: no unsafe rows; safe to flip oauth_scope_enforcement_enabled=true.")
	} else {
		fmt.Println("UNSAFE: resolve the rows above before enabling enforcement.")
	}
}

// sampleSuffix renders "(sample: 12, 47, 81)" for an ID slice. IDs are
// internal numerics, not credentials, so no further redaction is needed
// for this CLI.
func sampleSuffix(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	out := "(sample:"
	for i, id := range ids {
		if i == 0 {
			out += fmt.Sprintf(" %d", id)
		} else {
			out += fmt.Sprintf(", %d", id)
		}
	}
	return out + ")"
}
