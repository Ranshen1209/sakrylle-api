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

// OAuthAccessToken is OAuth v2 metadata for an issued access token. The
// access-token plaintext lives on api_keys (with a sk_oauth_ prefix and
// expires_at set); this row carries the OAuth-specific fields that gateway
// middleware, /v1/me, and the Authorized Apps page need.
//
// One row per active access token; UNIQUE(api_key_id) enforces a 1:1
// mapping. Refresh-token rotation creates a new api_keys row + a new
// oauth_access_tokens row in the same transaction; revocation marks
// revoked_at on this row AND disables the api_keys row + invalidates the
// auth cache by plaintext key.
//
// See docs/OAUTH_V2_DESIGN.md §10.5.
type OAuthAccessToken struct {
	ent.Schema
}

func (OAuthAccessToken) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_access_tokens"},
	}
}

func (OAuthAccessToken) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthAccessToken) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("api_key_id").
			Unique().
			Comment("api_keys.id holding the access-token plaintext (sk_oauth_*)"),
		field.String("grant_id").
			MaxLen(64).
			NotEmpty().
			Comment("Grant identity (UUID); same value across rotations of this grant"),
		field.String("token_family_id").
			MaxLen(64).
			NotEmpty().
			Comment("Family identity (UUID); same value across rotations of this token family"),
		field.String("client_id").
			MaxLen(128).
			NotEmpty(),
		field.Int64("user_id"),
		field.JSON("scopes", []string{}).
			Default([]string{}),
		field.Int64("group_id").
			Comment("Current group bound to this access token"),
		field.JSON("allowed_groups_snapshot", []int64{}).
			Default([]int64{}),
		field.String("app_type").
			MaxLen(32).
			Default("unknown"),
		field.String("device_id").
			MaxLen(128).
			Optional().
			Nillable(),
		field.String("device_name").
			MaxLen(200).
			Optional().
			Nillable(),
		field.Time("issued_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("Service layer sets explicitly; SQL default NOW() is a safety net"),
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_used_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("last_used_ip").
			MaxLen(64).
			Optional().
			Nillable(),
		field.Text("last_used_user_agent").
			Optional().
			Nillable(),
		field.Time("revoked_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (OAuthAccessToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_id"),
		index.Fields("user_id"),
		index.Fields("grant_id"),
		index.Fields("token_family_id"),
		index.Fields("expires_at"),
		index.Fields("revoked_at"),
	}
}
