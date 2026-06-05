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

// OAuthCode holds short-lived OAuth 2.0 authorization codes (RFC 6749 §4.1).
//
// Stored as SHA-256(code) so a DB leak never exposes a usable code.
//
// v2 (migration 145) adds group_id, grant_id, allowed_groups_snapshot,
// device_id, and device_name. New code rows MUST have non-empty grant_id and
// non-empty allowed_groups_snapshot; legacy rows created before migration may
// still be consumed via the compatibility path. See §10.2.
type OAuthCode struct {
	ent.Schema
}

func (OAuthCode) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_codes"},
	}
}

func (OAuthCode) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthCode) Fields() []ent.Field {
	return []ent.Field{
		field.String("code_hash").
			MaxLen(64).
			NotEmpty().
			Unique().
			Comment("hex-encoded SHA-256 of the issued code"),
		field.String("client_id").
			MaxLen(128).
			NotEmpty(),
		field.Int64("user_id"),
		field.String("redirect_uri").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			NotEmpty(),
		field.JSON("scopes", []string{}),
		field.String("code_challenge").
			MaxLen(128).
			NotEmpty().
			Comment("PKCE code_challenge value"),
		field.String("code_challenge_method").
			MaxLen(10).
			Default("S256"),
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("used_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),

		// ── v2 (migration 145) ────────────────────────────────────────────

		field.Int64("group_id").
			Optional().
			Nillable().
			Comment("Group selected during approval; persisted for token mint"),
		field.String("grant_id").
			MaxLen(64).
			Optional().
			Nillable().
			Comment("Grant identity; new approvals MUST set this (UUID)"),
		field.JSON("allowed_groups_snapshot", []int64{}).
			Default([]int64{}).
			Comment("Group IDs the user consented to for this grant"),
		field.String("device_id").
			MaxLen(128).
			Optional().
			Nillable().
			Comment("Optional client-provided install/session identifier"),
		field.String("device_name").
			MaxLen(200).
			Optional().
			Nillable().
			Comment("Sanitized device display name shown in Authorized Apps"),

		// ── OIDC (migration 149) ──────────────────────────────────────────

		field.String("nonce").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Optional().
			Comment("OIDC nonce from the authorize request; echoed in id_token nonce claim"),

		// ── OIDC session ID (migration 158) ────────────────────────────
		field.String("sid").
			Optional().
			Comment("OIDC session identifier; copied from authorize transaction at code mint"),
	}
}

func (OAuthCode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
		index.Fields("user_id"),
		index.Fields("client_id"),
		index.Fields("grant_id"),
	}
}
