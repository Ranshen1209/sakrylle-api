package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// OAuthClient holds the schema for an OAuth 2.0 client registered with this server.
//
// v2 (migration 145) extends the row with client_type, app_type, default_scopes,
// allowed_group_ids, allowed_origins, logout_redirect_uris, device_flow_enabled,
// allow_refresh_without_offline_access, and a few descriptive URL columns. See
// docs/OAUTH_V2_DESIGN.md §10.1 for the application-level invariants
// (client_type write-once, allowed_group_ids non-empty when present, etc.).
// Those invariants are NOT expressed in the Ent schema; they are enforced at
// the service layer because Ent has no native "write-once" or
// "non-empty array" support.
type OAuthClient struct {
	ent.Schema
}

func (OAuthClient) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_clients"},
	}
}

func (OAuthClient) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthClient) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_id").
			MaxLen(128).
			NotEmpty().
			Unique(),
		field.String("name").
			MaxLen(200).
			NotEmpty(),
		field.String("client_secret_hash").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Optional().
			Nillable().
			Comment("bcrypt of client_secret; null when pkce_required=true"),
		field.JSON("redirect_uris", []string{}).
			Comment("Whitelist of allowed redirect URIs (exact match)"),
		field.JSON("allowed_scopes", []string{}).
			Comment("Scopes this client may request"),
		field.Bool("pkce_required").
			Default(true),
		field.Int64("default_group_id").
			Optional().
			Nillable().
			Comment("Issued access tokens are bound to this group; falls back to setting oauth_default_group_id"),
		field.Int("access_token_ttl_seconds").
			Default(86400).
			Comment("access_token lifetime in seconds (default 24h)"),
		field.Int("refresh_token_ttl_seconds").
			Default(2592000).
			Comment("refresh_token lifetime in seconds (default 30d)"),
		field.Bool("disabled").
			Default(false),

		// ── v2 (migration 145) ────────────────────────────────────────────

		// client_type: "public" or "confidential". Service layer enforces
		// write-once + cross-field rules (confidential => secret hash).
		field.String("client_type").
			MaxLen(32).
			Default("public").
			Comment("public | confidential; write-once, validated by service layer"),
		// app_type: see docs/OAUTH_V2_DESIGN.md §8.1.
		field.String("app_type").
			MaxLen(32).
			Default("unknown").
			Comment("Categorizes UX/storage expectations: web, native, cli, image, ..."),
		field.Bool("trusted_first_party").
			Default(false).
			Comment("First-party Sakrylle clients may bypass per-grant consent under §9"),
		field.JSON("default_scopes", []string{}).
			Default([]string{}).
			Comment("Used when /authorize requests no scope; subset of allowed_scopes"),
		// allowed_group_ids: NULL means "no client-level restriction"; when
		// non-NULL the migration's CHECK enforces non-empty.
		field.JSON("allowed_group_ids", []int64{}).
			Optional().
			Comment("Optional client-level group whitelist; NULL = unrestricted at client layer"),
		field.JSON("allowed_origins", []string{}).
			Default([]string{}).
			Comment("CORS origin allowlist for token endpoint and PKCE clients"),
		field.JSON("logout_redirect_uris", []string{}).
			Default([]string{}).
			Comment("Whitelist of allowed post-logout redirect URIs"),
		field.Bool("device_flow_enabled").
			Default(false).
			Comment("Allow this client to use the RFC 8628 device authorization grant"),
		field.Bool("allow_refresh_without_offline_access").
			Default(false).
			Comment("Legacy compat: mint refresh tokens even when offline_access not granted"),
		field.Text("icon_url").
			Optional().
			Nillable(),
		field.Text("homepage_url").
			Optional().
			Nillable(),
		field.Text("privacy_url").
			Optional().
			Nillable(),
		field.Text("terms_url").
			Optional().
			Nillable(),
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("disabled"),
	}
}
