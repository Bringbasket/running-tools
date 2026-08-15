package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthAPIToken stores revocable non-browser access credentials by digest.
type AuthAPIToken struct{ ent.Schema }

func (AuthAPIToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").MaxLen(64).Immutable(),
		field.Int64("user_id").Immutable(),
		field.String("name").NotEmpty().MaxLen(100),
		field.String("token_hash").Sensitive().MaxLen(64).Unique().Immutable(),
		field.String("token_prefix").MaxLen(20).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (AuthAPIToken) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "created_at")} }
