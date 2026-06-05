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

		// ── OIDC id_token signing (migration 151) ─────────────────────────
		// signing_algorithm selects the JWS alg for this client's id_tokens.
		// The DB CHECK constraint restricts values to RS256/ES256; the service
		// layer (resolveSigningAlgorithm) additionally fails safe to RS256.
		field.String("signing_algorithm").
			MaxLen(16).
			Default("RS256").
			Comment("JWS algorithm for id_token signing: RS256 or ES256; defaults to RS256"),

		// ── OIDC request_uri (migration 155) ──────────────────────────────
		// request_uris lists pre-registered HTTPS URIs from which the client's
		// request objects may be fetched (OIDC Core §6.3). Each entry MUST use
		// the https scheme (enforced at the application layer).
		field.JSON("request_uris", []string{}).
			Default([]string{}).
			Comment("Pre-registered HTTPS URIs for fetching request objects; empty = not supported"),

		// ── OIDC back-channel logout (migration 156) ──────────────────────
		// backchannel_logout_uri receives logout_token POSTs when the user
		// logs out via RP-Initiated Logout. NULL = no back-channel notification.
		field.Text("backchannel_logout_uri").
			Optional().
			Nillable().
			Comment("URI that receives a logout_token POST on user logout; NULL means no back-channel notification"),
		// backchannel_logout_session_required: when true, the OP includes a sid
		// claim in id_tokens so the RP can identify the session in logout tokens.
		field.Bool("backchannel_logout_session_required").
			Default(false).
			Comment("When true, include sid claim in id_tokens for back-channel logout"),

		// ── OIDC pairwise subject (migration 153) ─────────────────────────
		// subject_type controls whether the sub claim is the user's stable ID
		// ("public") or a per-client pseudonym ("pairwise", OIDC Core §8).
		// The DB CHECK constraint restricts values to public|pairwise.
		field.String("subject_type").
			MaxLen(16).
			Default("public").
			Comment("Subject identifier type: public (same sub for all clients) or pairwise (per-client pseudonym)"),
		// sector_identifier_uri is the URL from which the client's sector
		// identifier is fetched. When empty, the sector identifier is derived
		// from the host portion of the client's redirect_uris (OIDC Core §8.1).
		field.Text("sector_identifier_uri").
			Optional().
			Nillable().
			Comment("URI for fetching the sector identifier JSON document; empty means use redirect_uris hosts"),

		// ── OIDC front-channel logout (migration 161) ─────────────────────
		// frontchannel_logout_uri is rendered as a hidden iframe during
		// front-channel logout so the RP can clear its session state.
		// NULL = no front-channel notification for this client.
		field.Text("frontchannel_logout_uri").
			Optional().
			Nillable().
			Comment("URI rendered as a hidden iframe on front-channel logout; NULL means no front-channel notification"),
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("disabled"),
	}
}
