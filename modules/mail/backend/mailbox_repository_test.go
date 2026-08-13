package mail

import (
	"context"
	"testing"
)

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
	if err := repository.Save(context.Background(), mailboxCache{Messages: []MailMessage{{UID: 1}}}); err != nil { t.Fatal(err) }
	if err := repository.Clear(context.Background()); err != nil { t.Fatal(err) }
	loaded, _, err := repository.Load(context.Background())
	if err != nil || len(loaded.Messages) != 0 { t.Fatalf("cache not cleared: %#v err=%v", loaded, err) }
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
