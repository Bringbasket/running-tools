package mail

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresMailboxRepositoryCompactQueries(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RUNNING_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("RUNNING_TEST_DATABASE_URL is not configured")
	}
	ctx := context.Background()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect test PostgreSQL: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TEMP TABLE mailbox_messages (
			id BIGINT PRIMARY KEY, account_id VARCHAR(64) NOT NULL, generation VARCHAR(128) NOT NULL,
			uid BIGINT NOT NULL, aliases JSONB NOT NULL, from_address VARCHAR(1000) NOT NULL,
			subject VARCHAR(2000) NOT NULL, message_date DOUBLE PRECISION NOT NULL, text TEXT NOT NULL,
			safe_html TEXT NOT NULL, codes JSONB NOT NULL, partner_codes JSONB NOT NULL
		) ON COMMIT DROP`,
		`CREATE TEMP TABLE mailbox_hidden_messages (
			id BIGINT PRIMARY KEY, account_id VARCHAR(64) NOT NULL, generation VARCHAR(128) NOT NULL,
			alias VARCHAR(320) NOT NULL, uid BIGINT NOT NULL
		) ON COMMIT DROP`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	longText := strings.Repeat("验", 200)
	if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_messages
		(id, account_id, generation, uid, aliases, from_address, subject, message_date, text, safe_html, codes, partner_codes)
		VALUES
		(1, 'account-one', 'generation-1', 1, '["one@icloud.com"]'::jsonb, 'first@example.com', 'first', 100, $1, '<p>one</p>', '["111111"]'::jsonb, '[]'::jsonb),
		(2, 'account-one', 'generation-1', 2, '["one@icloud.com", "two@icloud.com"]'::jsonb, 'second@example.com', 'second', 200, $2, '<p>two</p>', '[]'::jsonb, '["222222"]'::jsonb),
		(3, 'account-two', 'generation-1', 3, '["one@icloud.com"]'::jsonb, 'other@example.com', 'other', 300, 'other', '<p>other</p>', '[]'::jsonb, '[]'::jsonb)`, longText, longText); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_hidden_messages
		(id, account_id, generation, alias, uid) VALUES
		(1, 'account-one', 'generation-1', 'one@icloud.com', 2)`); err != nil {
		t.Fatal(err)
	}

	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx})
	client := ent.NewClient(ent.Driver(driver))
	repository := &postgresMailboxRepository{client: client, accountID: "account-one"}

	one, err := repository.ListAliasMessages(ctx, "generation-1", "one@icloud.com", 1000, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].UID != 1 || one[0].SafeHTML != "" || !utf8.ValidString(one[0].Text) || len([]rune(one[0].Text)) != mailboxMessagePreviewSize {
		t.Fatalf("unexpected compact alias result: %#v", one)
	}

	recent, err := repository.ListRecentMessages(ctx, "generation-1", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].UID != 2 || recent[1].UID != 1 || recent[0].SafeHTML != "" {
		t.Fatalf("unexpected recent result: %#v", recent)
	}

	if _, ok, err := repository.GetMessage(ctx, "generation-1", "one@icloud.com", 2); err != nil || ok {
		t.Fatalf("hidden alias resolved message: ok=%v err=%v", ok, err)
	}
	detail, ok, err := repository.GetMessage(ctx, "generation-1", "two@icloud.com", 2)
	if err != nil || !ok || detail.SafeHTML != "<p>two</p>" || len([]rune(detail.Text)) != 200 {
		t.Fatalf("unexpected full detail: ok=%v message=%#v err=%v", ok, detail, err)
	}
}
