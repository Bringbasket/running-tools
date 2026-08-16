package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MailAliasAppState stores application-registration evidence derived from IMAP.
type MailAliasAppState struct{ ent.Schema }

func (MailAliasAppState) Fields() []ent.Field {
	return []ent.Field{
		field.String("account_id").Default("default").MaxLen(64),
		field.String("alias").NotEmpty().MaxLen(320),
		field.String("app_key").NotEmpty().MaxLen(64),
		field.String("status").NotEmpty().MaxLen(24),
		field.Time("detected_at").Optional().Nillable(),
		field.Uint64("detected_uid").Default(0),
		field.String("detected_subject").Default("").MaxLen(500),
		field.String("detected_sender").Default("").MaxLen(1000),
		field.Time("confirmed_at").Optional().Nillable(),
		field.Uint64("confirmed_uid").Default(0),
		field.String("confirmed_subject").Default("").MaxLen(500),
		field.String("confirmed_sender").Default("").MaxLen(1000),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (MailAliasAppState) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "alias", "app_key").Unique(),
		index.Fields("account_id", "status", "updated_at"),
	}
}
