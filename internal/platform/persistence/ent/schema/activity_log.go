package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ActivityLog is an immutable, module-scoped user and background operation log.
type ActivityLog struct {
	ent.Schema
}

func (ActivityLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("module").NotEmpty().MaxLen(64).Immutable(),
		field.String("account_id").Default("default").MaxLen(64).Immutable(),
		field.String("category").NotEmpty().MaxLen(64).Immutable(),
		field.String("action").NotEmpty().MaxLen(128).Immutable(),
		field.String("level").NotEmpty().MaxLen(16).Immutable(),
		field.String("outcome").NotEmpty().MaxLen(16).Immutable(),
		field.String("summary").NotEmpty().MaxLen(500).Immutable(),
		field.String("source").NotEmpty().MaxLen(32).Immutable(),
		field.String("method").Default("").MaxLen(16).Immutable(),
		field.String("path").Default("").MaxLen(500).Immutable(),
		field.Int("http_status").Default(0).Immutable(),
		field.Int64("duration_ms").Default(0).Immutable(),
		field.String("request_id").Default("").MaxLen(128).Immutable(),
		field.String("detail").Default("").MaxLen(2000).Immutable(),
		field.JSON("metadata", map[string]any{}).Default(map[string]any{}).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable(),
	}
}

func (ActivityLog) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("module", "account_id", "created_at"),
		index.Fields("module", "account_id", "category", "created_at"),
		index.Fields("module", "account_id", "level", "created_at"),
		index.Fields("request_id"),
	}
}
