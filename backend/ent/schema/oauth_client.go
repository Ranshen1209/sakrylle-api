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
	}
}

func (OAuthClient) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("disabled"),
	}
}
