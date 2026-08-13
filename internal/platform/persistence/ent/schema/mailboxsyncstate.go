package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MailboxSyncState struct {
	ent.Schema
}

func (MailboxSyncState) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().MaxLen(64),
		field.JSON("status", map[string]any{}).Default(map[string]any{}),
		field.Uint64("highest_uid").Default(0),
		field.JSON("allowed_aliases", []string{}).Default([]string{}),
	}
}

func (MailboxSyncState) Indexes() []ent.Index {
	return []ent.Index{index.Fields("key").Unique()}
}
