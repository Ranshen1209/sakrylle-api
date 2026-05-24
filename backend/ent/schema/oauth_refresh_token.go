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

// OAuthRefreshToken links a refresh_token (rotated on each use) to the
// api_key row that holds the matching access_token.
//
// Stored as SHA-256(token); rotation marks the old row revoked and inserts a new one.
type OAuthRefreshToken struct {
	ent.Schema
}

func (OAuthRefreshToken) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_refresh_tokens"},
	}
}

func (OAuthRefreshToken) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthRefreshToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("token_hash").
			MaxLen(64).
			NotEmpty().
			Unique(),
		field.String("client_id").
			MaxLen(128).
			NotEmpty(),
		field.Int64("user_id"),
		field.Int64("api_key_id").
			Comment("api_keys.id for the access_token this refresh token can rotate"),
		field.JSON("scopes", []string{}),
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("revoked_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("rotated_to_hash").
			MaxLen(64).
			Optional().
			Nillable().
			Comment("token_hash of the row that replaced this one (for replay detection)"),
	}
}

func (OAuthRefreshToken) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
		index.Fields("user_id"),
		index.Fields("client_id"),
		index.Fields("api_key_id"),
	}
}
