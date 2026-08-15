package mail

import (
	"context"
	"strings"
	"testing"
	"time"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/Bringbasket/running-tools/internal/platform/persistence/ent/mailboxmessage"
)

type mailboxQuerySpy struct {
	state          mailboxCache
	loadCalls      int
	loadStateCalls int
	aliasCalls     int
	recentCalls    int
	messageCalls   int
}

func (repository *mailboxQuerySpy) Load(context.Context) (mailboxCache, bool, error) {
	repository.loadCalls++
	return repository.state, true, nil
}

func (repository *mailboxQuerySpy) LoadState(context.Context) (mailboxCache, bool, error) {
	repository.loadStateCalls++
	return repository.state, true, nil
}

func (*mailboxQuerySpy) Save(context.Context, mailboxCache) error      { return nil }
func (*mailboxQuerySpy) SaveState(context.Context, mailboxCache) error { return nil }
func (repository *mailboxQuerySpy) ListAliasMessages(context.Context, string, string, int, bool) ([]MailMessage, error) {
	repository.aliasCalls++
	return []MailMessage{}, nil
}
func (repository *mailboxQuerySpy) ListRecentMessages(context.Context, string, float64, int) ([]MailMessage, error) {
	repository.recentCalls++
	return []MailMessage{}, nil
}
func (repository *mailboxQuerySpy) GetMessage(context.Context, string, string, uint32) (MailMessage, bool, error) {
	repository.messageCalls++
	return MailMessage{}, false, nil
}
func (*mailboxQuerySpy) Clear(context.Context) error { return nil }

func TestJSONMailboxRepositoryRoundTrip(t *testing.T) {
	repository := &jsonMailboxRepository{path: t.TempDir() + "/mailbox-cache.json"}
	cache := mailboxCache{
		Status:   MailboxStatus{MailboxGeneration: "generation-1", Revision: 4},
		Messages: []MailMessage{{UID: 12, Aliases: []string{"one@icloud.com"}, Text: "full body"}},
		Hidden:   []string{messageKey("generation-1", "one@icloud.com", 12)},
	}
	if err := repository.Save(context.Background(), cache); err != nil {
		t.Fatal(err)
	}
	loaded, exists, err := repository.Load(context.Background())
	if err != nil || !exists {
		t.Fatalf("load failed: exists=%v err=%v", exists, err)
	}
	if loaded.Status.Revision != 4 || len(loaded.Messages) != 1 || loaded.Messages[0].Text != "full body" || len(loaded.Hidden) != 1 {
		t.Fatalf("unexpected cache: %#v", loaded)
	}
}

func TestJSONMailboxRepositoryClear(t *testing.T) {
	repository := &jsonMailboxRepository{path: t.TempDir() + "/mailbox-cache.json"}
	if err := repository.Save(context.Background(), mailboxCache{Messages: []MailMessage{{UID: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := repository.Load(context.Background())
	if err != nil || len(loaded.Messages) != 0 {
		t.Fatalf("cache not cleared: %#v err=%v", loaded, err)
	}
}

func TestParseMessageKey(t *testing.T) {
	alias, uid, ok := parseMessageKey("generation-1", "generation-1:one@icloud.com:429")
	if !ok || alias != "one@icloud.com" || uid != 429 {
		t.Fatalf("unexpected parsed key: %q %d %v", alias, uid, ok)
	}
	if _, _, ok := parseMessageKey("other-generation", "generation-1:one@icloud.com:429"); ok {
		t.Fatal("accepted a hidden key from another mailbox generation")
	}
}

func TestJSONMailboxRepositoryStateOnlyOperationsPreserveMessages(t *testing.T) {
	repository := &jsonMailboxRepository{path: t.TempDir() + "/mailbox-cache.json"}
	cache := mailboxCache{
		Status:         MailboxStatus{MailboxGeneration: "generation-1", Revision: 4},
		Messages:       []MailMessage{{UID: 12, Aliases: []string{"one@icloud.com"}, Text: "full body"}},
		Hidden:         []string{messageKey("generation-1", "one@icloud.com", 12)},
		HighestUID:     12,
		AllowedAliases: []string{"one@icloud.com"},
	}
	if err := repository.Save(context.Background(), cache); err != nil {
		t.Fatal(err)
	}

	state, exists, err := repository.LoadState(context.Background())
	if err != nil || !exists {
		t.Fatalf("load state failed: exists=%v err=%v", exists, err)
	}
	if state.Status.Revision != 4 || state.HighestUID != 12 || len(state.Messages) != 0 || len(state.Hidden) != 0 {
		t.Fatalf("state-only load returned unexpected data: %#v", state)
	}

	state.Status.Revision = 5
	state.HighestUID = 20
	state.AllowedAliases = []string{"one@icloud.com", "two@icloud.com"}
	if err := repository.SaveState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	loaded, _, err := repository.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status.Revision != 5 || loaded.HighestUID != 20 || len(loaded.AllowedAliases) != 2 {
		t.Fatalf("state was not updated: %#v", loaded)
	}
	if len(loaded.Messages) != 1 || loaded.Messages[0].Text != "full body" || len(loaded.Hidden) != 1 {
		t.Fatalf("state-only save changed message data: %#v", loaded)
	}
}

func TestJSONMailboxRepositoryAliasQueryIsNewestFirstHiddenAndBounded(t *testing.T) {
	repository := &jsonMailboxRepository{path: t.TempDir() + "/mailbox-cache.json"}
	messages := make([]MailMessage, 0, 130)
	for uid := uint32(1); uid <= 130; uid++ {
		messages = append(messages, MailMessage{
			UID: uid, Aliases: []string{"target@icloud.com"}, Date: float64(uid),
			Text: strings.Repeat("x", 300), SafeHTML: "<strong>full</strong>",
		})
	}
	cache := mailboxCache{
		Status:   MailboxStatus{MailboxGeneration: "generation-1"},
		Messages: messages,
		Hidden:   []string{messageKey("generation-1", "target@icloud.com", 130)},
	}
	if err := repository.Save(context.Background(), cache); err != nil {
		t.Fatal(err)
	}

	listed, err := repository.ListAliasMessages(context.Background(), "generation-1", "TARGET@ICLOUD.COM", 1000, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != mailboxAliasQueryMaximum || listed[0].UID != 129 || listed[len(listed)-1].UID != 30 {
		t.Fatalf("unexpected bounded alias list: count=%d first=%d last=%d", len(listed), listed[0].UID, listed[len(listed)-1].UID)
	}
	if len([]rune(listed[0].Text)) > mailboxMessagePreviewSize || listed[0].SafeHTML != "" {
		t.Fatalf("compact query exposed a full body: %#v", listed[0])
	}

	detailed, err := repository.ListAliasMessages(context.Background(), "generation-1", "target@icloud.com", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(detailed) != 1 || detailed[0].UID != 129 || detailed[0].SafeHTML == "" || len(detailed[0].Text) != 300 {
		t.Fatalf("detailed query lost the message body: %#v", detailed)
	}
}

func TestJSONMailboxRepositoryRecentAndMessageRespectPerAliasHiding(t *testing.T) {
	repository := &jsonMailboxRepository{path: t.TempDir() + "/mailbox-cache.json"}
	cache := mailboxCache{
		Status: MailboxStatus{MailboxGeneration: "generation-1"},
		Messages: []MailMessage{
			{UID: 1, Aliases: []string{"one@icloud.com"}, Date: 100},
			{UID: 2, Aliases: []string{"one@icloud.com"}, Date: 200},
			{UID: 3, Aliases: []string{"one@icloud.com", "two@icloud.com"}, Date: 300, SafeHTML: "<p>detail</p>"},
			{UID: 4, Aliases: []string{"two@icloud.com"}, Date: 400},
		},
		Hidden: []string{
			messageKey("generation-1", "one@icloud.com", 2),
			messageKey("generation-1", "one@icloud.com", 3),
		},
	}
	if err := repository.Save(context.Background(), cache); err != nil {
		t.Fatal(err)
	}

	recent, err := repository.ListRecentMessages(context.Background(), "generation-1", 150, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].UID != 4 || recent[1].UID != 3 {
		t.Fatalf("recent visibility mismatch: %#v", recent)
	}
	if recent[1].SafeHTML != "" {
		t.Fatal("recent query exposed full HTML")
	}

	if _, ok, err := repository.GetMessage(context.Background(), "generation-1", "one@icloud.com", 3); err != nil || ok {
		t.Fatalf("hidden alias unexpectedly resolved message: ok=%v err=%v", ok, err)
	}
	message, ok, err := repository.GetMessage(context.Background(), "generation-1", "two@icloud.com", 3)
	if err != nil || !ok || message.SafeHTML == "" {
		t.Fatalf("visible alias did not resolve detail: ok=%v message=%#v err=%v", ok, message, err)
	}
}

func TestPostgresMailboxPredicatesStayAccountScoped(t *testing.T) {
	table := entsql.Table(mailboxmessage.Table)
	selector := entsql.Dialect("postgres").Select(table.Columns(mailboxmessage.Columns...)...).From(table)
	mailboxMessageHasAlias("one@icloud.com")(selector)
	mailboxMessageNotHiddenForAlias("account-one", "generation-1", "one@icloud.com")(selector)
	mailboxMessageHasVisibleAlias()(selector)
	query, args := selector.Query()

	for _, fragment := range []string{"@>", "jsonb_array_elements_text", "mailbox_hidden_messages", "account_id", "generation", "uid"} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query is missing %q: %s", fragment, query)
		}
	}
	joinedArgs := make([]string, 0, len(args))
	for _, argument := range args {
		joinedArgs = append(joinedArgs, argument.(string))
	}
	arguments := strings.Join(joinedArgs, " ")
	for _, expected := range []string{"one@icloud.com", "account-one", "generation-1"} {
		if !strings.Contains(arguments, expected) {
			t.Fatalf("query arguments are missing %q: %#v", expected, args)
		}
	}
}

func TestMailboxReadEndpointsUseStateAndBoundedQueriesInsteadOfFullCache(t *testing.T) {
	repository := &mailboxQuerySpy{state: mailboxCache{Status: MailboxStatus{MailboxGeneration: "generation-1", Revision: 7}}}
	service := NewMailboxService(t.TempDir(), nil)
	service.repository = repository

	_ = service.Status()
	_ = service.Messages("one@icloud.com", 100)
	_ = service.Recent(500)
	_, _ = service.Message("one@icloud.com", 12)
	_ = service.WaitForRevision(7, time.Millisecond)

	if repository.loadCalls != 0 {
		t.Fatalf("read endpoints loaded the full mailbox cache %d times", repository.loadCalls)
	}
	if repository.loadStateCalls < 5 || repository.aliasCalls != 1 || repository.recentCalls != 1 || repository.messageCalls != 1 {
		t.Fatalf("unexpected bounded query calls: %#v", repository)
	}
}
