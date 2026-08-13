package activitylog

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestJSONStoreFiltersPaginatesAndRemovesSensitiveMetadata(t *testing.T) {
	store := New("mail", t.TempDir(), nil)
	store.Record(context.Background(), Input{Category: "session", Action: "session.check", Summary: "Session 检查成功", Source: "user", RequestID: "request-one", Metadata: map[string]any{"count": 2, "cookie": "must-not-save"}})
	store.Record(context.Background(), Input{Category: "mailbox", Action: "mailbox.sync", Level: "error", Outcome: "failure", Summary: "IMAP 同步失败", Source: "background", RequestID: "request-two"})

	page, err := store.Query(context.Background(), Query{Page: 1, PageSize: 10, Level: "error", Search: "同步"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Action != "mailbox.sync" || page.Stats.Failures24 != 1 || page.Stats.Background != 1 {
		t.Fatalf("unexpected query result: %#v", page)
	}
	all, err := store.Query(context.Background(), Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := all.Items[1].Metadata["cookie"]; exists {
		t.Fatal("sensitive metadata was persisted")
	}
}

func TestJSONStoreUsesServerSideTimeRange(t *testing.T) {
	store := New("mail", t.TempDir(), nil)
	store.Record(context.Background(), Input{Category: "alias", Action: "alias.list", Summary: "读取邮箱列表", Source: "user"})
	future := time.Now().Add(time.Hour)
	page, err := store.Query(context.Background(), Query{Page: 1, PageSize: 20, StartTime: &future})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("expected empty future range, got %#v", page)
	}
}

func TestJSONStoreClearRemovesModuleRecords(t *testing.T) {
	store := New("mail", t.TempDir(), nil)
	store.Record(context.Background(), Input{Category: "alias", Action: "alias.list", Summary: "列表", Source: "user"})
	if err := store.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := store.Query(context.Background(), Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("expected cleared log store, got %#v", page)
	}
}

func TestLogDetailRedactsSensitiveValues(t *testing.T) {
	store := New("mail", t.TempDir(), nil)
	store.Record(context.Background(), Input{Category: "session", Action: "session.check", Level: "error", Outcome: "failure",
		Summary: "Session 检查失败", Source: "user", Detail: `HTTP 401 Cookie: private; token="secret-value" password=guess`})
	page, err := store.Query(context.Background(), Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	detail := page.Items[0].Detail
	if strings.Contains(detail, "private") || strings.Contains(detail, "secret-value") || strings.Contains(detail, "guess") {
		t.Fatalf("sensitive detail was persisted: %q", detail)
	}
	if !strings.Contains(detail, "HTTP 401") || !strings.Contains(detail, "<redacted>") {
		t.Fatalf("diagnostic detail was removed: %q", detail)
	}
}
