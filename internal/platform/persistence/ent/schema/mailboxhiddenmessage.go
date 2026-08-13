package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MailboxHiddenMessage struct {
	ent.Schema
}

func (MailboxHiddenMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("account_id").Default("default").MaxLen(64),
		field.String("generation").NotEmpty().MaxLen(128),
		field.String("alias").NotEmpty().MaxLen(320),
		field.Uint64("uid"),
	}
}

func (MailboxHiddenMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "generation", "alias", "uid").Unique(),
		index.Fields("account_id", "generation", "uid"),
	}
}
