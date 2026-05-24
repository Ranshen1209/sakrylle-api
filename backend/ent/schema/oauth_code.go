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
	}
}

func (OAuthCode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
		index.Fields("user_id"),
		index.Fields("client_id"),
	}
}
