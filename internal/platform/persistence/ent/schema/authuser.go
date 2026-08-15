package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthUser stores a management-console identity. Passwords are Argon2id hashes.
type AuthUser struct{ ent.Schema }

func (AuthUser) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.String("username").NotEmpty().MaxLen(64).Unique(),
		field.String("password_hash").Sensitive(),
		field.Bool("must_change_password").Default(false),
		field.Bool("disabled").Default(false),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Time("last_login_at").Optional().Nillable(),
	}
}

func (AuthUser) Indexes() []ent.Index { return []ent.Index{index.Fields("username").Unique()} }
