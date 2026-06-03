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

// OAuthAuthorizeTransaction is the server-side state for an OAuth 2.0
// /authorize request that has been validated and rendered as a consent page.
// Approval and denial both look up this row by transaction_id, verify
// csrf_hash, and mark consumed_at — the browser body MUST NOT be allowed to
// alter client_id, redirect_uri, scopes, state, or PKCE fields.
//
// See docs/OAUTH_V2_DESIGN.md §10.3.
type OAuthAuthorizeTransaction struct {
	ent.Schema
}

func (OAuthAuthorizeTransaction) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_authorize_transactions"},
	}
}

func (OAuthAuthorizeTransaction) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthAuthorizeTransaction) Fields() []ent.Field {
	return []ent.Field{
		field.String("transaction_id").
			MaxLen(96).
			NotEmpty().
			Unique().
			Comment("Random opaque value (≥32 random bytes before base64url)"),
		field.String("csrf_hash").
			MaxLen(64).
			NotEmpty().
			Comment("SHA-256(plaintext CSRF); plaintext is returned only in rendered consent page"),
		field.Int64("user_id").
			Comment("Authenticated user id captured at /authorize time; approve must reject mismatched JWT subject"),
		field.String("client_id").
			MaxLen(128).
			NotEmpty(),
		field.Text("redirect_uri").
			NotEmpty(),
		field.String("response_type").
			MaxLen(32).
			Default("code"),
		field.JSON("scopes", []string{}).
			Default([]string{}),
		field.JSON("allowed_groups_snapshot", []int64{}).
			Default([]int64{}).
			Comment("Filtered group set rendered in consent UI; used for group-selector validation"),
		field.Text("state").
			Comment("Opaque client state echoed back on redirect; not validated server-side"),
		field.String("code_challenge").
			MaxLen(128).
			NotEmpty(),
		field.String("code_challenge_method").
			MaxLen(10).
			Default("S256"),
		field.Int64("requested_group_id").
			Optional().
			Nillable().
			Comment("Optional group selected in the UI before approval"),
		field.String("device_id").
			MaxLen(128).
			Optional().
			Nillable(),
		field.String("device_name").
			MaxLen(200).
			Optional().
			Nillable(),
		field.Time("consumed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("Set under row lock by approve or deny; idempotency anchor"),
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("created_ip").
			MaxLen(64).
			Optional().
			Nillable(),
		field.Text("created_user_agent").
			Optional().
			Nillable(),

		// ── OIDC (migration 149) ──────────────────────────────────────────

		field.String("nonce").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Optional().
			Comment("OIDC nonce captured at /authorize; copied to the code, then the id_token"),
	}
}

func (OAuthAuthorizeTransaction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("expires_at"),
		index.Fields("client_id"),
		index.Fields("user_id"),
	}
}
