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

// OAuthDeviceCode is RFC 8628 device-flow state. We store SHA-256 hashes
// of device_code and user_code only; plaintext is returned exactly once at
// /oauth/device/code creation.
//
// state machine: pending → approved → consumed (token mint), or
//
//	pending → denied (5x failed approval, or explicit deny), or
//	pending → expired (poll/approval after expires_at)
//
// CHECK constraints in migration 145 ensure status / *_at timestamps cannot
// contradict each other; the partial unique index keeps user_code_hash
// unique across pending+approved rows but not across terminal states.
//
// See docs/OAUTH_V2_DESIGN.md §10.6.
type OAuthDeviceCode struct {
	ent.Schema
}

func (OAuthDeviceCode) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "oauth_device_codes"},
	}
}

func (OAuthDeviceCode) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (OAuthDeviceCode) Fields() []ent.Field {
	return []ent.Field{
		field.String("device_code_hash").
			MaxLen(64).
			NotEmpty().
			Unique().
			Comment("SHA-256(device_code); plaintext is returned only at create-time"),
		field.String("user_code_hash").
			MaxLen(64).
			NotEmpty().
			Comment("SHA-256(user_code); partial unique index in DB covers pending+approved only — see migration 145 (idx_oauth_device_codes_user_code_active_unique). Ent cannot express partial indexes natively, so the constraint lives in SQL only."),
		field.String("client_id").
			MaxLen(128).
			NotEmpty(),
		field.JSON("scopes", []string{}).
			Default([]string{}),
		field.String("grant_id").
			MaxLen(64).
			Optional().
			Nillable().
			Comment("Set on approval; used by token mint and grant-level revoke"),
		field.Int64("group_id").
			Optional().
			Nillable().
			Comment("Set on approval"),
		field.String("device_id").
			MaxLen(128).
			Optional().
			Nillable(),
		field.String("device_name").
			MaxLen(200).
			Optional().
			Nillable(),
		field.String("status").
			MaxLen(32).
			Default("pending").
			Comment("pending | approved | denied | consumed | expired"),
		field.Int("interval_seconds").
			Default(5).
			Comment("Minimum poll interval (RFC 8628)"),
		field.Int("poll_count").
			Default(0),
		field.Time("last_poll_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Int("slow_down_count").
			Default(0).
			Comment("Number of slow_down responses returned; service decides interval back-off"),
		field.Int("failed_user_code_attempts").
			Default(0).
			Comment("Wrong-user-code submissions; ≥5 transitions to denied"),
		field.Int64("approved_by_user_id").
			Optional().
			Nillable(),
		field.Time("approved_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("denied_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("consumed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("created_ip").
			MaxLen(64).
			Optional().
			Nillable(),
		field.Text("created_user_agent").
			Optional().
			Nillable(),
	}
}

func (OAuthDeviceCode) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_id"),
		index.Fields("user_code_hash"),
		index.Fields("expires_at"),
		index.Fields("approved_by_user_id"),
		index.Fields("status", "expires_at"),
		index.Fields("grant_id"),
	}
}
