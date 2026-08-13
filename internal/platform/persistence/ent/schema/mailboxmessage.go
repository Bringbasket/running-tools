package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MailboxMessage struct {
	ent.Schema
}

func (MailboxMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("generation").NotEmpty().MaxLen(128),
		field.Uint64("uid"),
		field.JSON("aliases", []string{}).Default([]string{}),
		field.String("from_address").Default("").MaxLen(1000),
		field.String("subject").Default("").MaxLen(2000),
		field.Float("message_date"),
		field.Text("text").Default(""),
		field.Text("safe_html").Default(""),
		field.JSON("codes", []string{}).Default([]string{}),
		field.JSON("partner_codes", []string{}).Default([]string{}),
	}
}

func (MailboxMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("generation", "uid").Unique(),
		index.Fields("generation", "message_date"),
	}
}
