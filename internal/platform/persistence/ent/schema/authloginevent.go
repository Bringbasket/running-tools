package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthLoginEvent is a bounded security audit record without credential material.
type AuthLoginEvent struct{ ent.Schema }

func (AuthLoginEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("user_id").Optional().Nillable().Immutable(),
		field.String("username").MaxLen(64).Immutable(),
		field.String("outcome").MaxLen(16).Immutable(),
		field.String("reason").Default("").MaxLen(64).Immutable(),
		field.String("ip_address").Default("").MaxLen(128).Immutable(),
		field.String("user_agent").Default("").MaxLen(500).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (AuthLoginEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("created_at"), index.Fields("username", "created_at")}
}
