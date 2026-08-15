package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// AuthSession stores only the SHA-256 digest of an opaque browser token.
type AuthSession struct{ ent.Schema }

func (AuthSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").StorageKey("token_hash").MaxLen(64).Immutable(),
		field.Int64("user_id").Immutable(),
		field.String("ip_address").Default("").MaxLen(128).Immutable(),
		field.String("user_agent").Default("").MaxLen(500).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("last_seen_at").Default(time.Now),
		field.Time("expires_at").Immutable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (AuthSession) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "expires_at")} }
