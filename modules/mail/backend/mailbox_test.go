package mail

import (
	"strings"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestParseMailMessageMatchesAliasAndExtractsCode(t *testing.T) {
	raw := "From: Service <service@example.com>\r\nTo: demo@icloud.com\r\nSubject: Your verification code is 482931\r\nDate: Tue, 12 Aug 2026 10:00:00 +0800\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p>验证码：482931</p>"
	message, err := parseMailMessage(7, []byte(raw), map[string]bool{"demo@icloud.com": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Aliases) != 1 || message.Aliases[0] != "demo@icloud.com" {
		t.Fatalf("alias mismatch: %#v", message)
	}
	if len(message.Codes) != 1 || message.Codes[0] != "482931" {
		t.Fatalf("code mismatch: %#v", message.Codes)
	}
	if strings.Contains(message.Text, "<p>") {
		t.Fatalf("html was not sanitized: %q", message.Text)
	}
}

func TestSanitizeEmailHTMLKeepsLayoutAndDropsActiveContent(t *testing.T) {
	raw := `<div style="background:url(https://tracker.example/pixel)"><script>alert(1)</script><img src="https://tracker.example/open"><a href="javascript:alert(2)" onclick="alert(3)">危险</a><a href="https://example.com/login?token=1" style="color:red">登录</a><form><input value="secret">表单</form><strong>验证码 123456</strong></div>`
	safe := sanitizeEmailHTML(raw)
	for _, forbidden := range []string{"script", "alert", "onclick", "style=", "tracker.example", "<img", "<form", "<input", "表单"} {
		if strings.Contains(strings.ToLower(safe), strings.ToLower(forbidden)) {
			t.Fatalf("unsafe content %q survived: %s", forbidden, safe)
		}
	}
	for _, expected := range []string{"<div>", "危险", `href="https://example.com/login?token=1"`, `rel="noopener noreferrer"`, "<strong>验证码 123456</strong>"} {
		if !strings.Contains(safe, expected) {
			t.Fatalf("expected %q in sanitized HTML: %s", expected, safe)
		}
	}
}

func TestMailMessageListDoesNotExposeSafeHTML(t *testing.T) {
	service := NewMailboxService(t.TempDir(), nil)
	cache := mailboxCache{
		Status:   MailboxStatus{MailboxGeneration: "generation-1"},
		Messages: []MailMessage{{UID: 11, Aliases: []string{"one@icloud.com"}, Text: "preview", SafeHTML: "<strong>private</strong>", Date: float64(time.Now().Unix())}},
	}
	if err := storage.WriteJSON(service.path, cache, 0o600); err != nil {
		t.Fatal(err)
	}
	listed := service.Messages("one@icloud.com", 20)["messages"].([]MailMessage)
	if len(listed) != 1 || listed[0].SafeHTML != "" {
		t.Fatalf("list exposed full HTML: %#v", listed)
	}
	detail, ok := service.Message("one@icloud.com", 11)
	if !ok || detail.SafeHTML == "" {
		t.Fatal("detail did not preserve safe HTML")
	}
}

func TestMergeMailMessagesIsIncrementalAndPrunesDisabledAliases(t *testing.T) {
	existing := []MailMessage{
		{UID: 10, Aliases: []string{"active@icloud.com"}, Date: 10},
		{UID: 11, Aliases: []string{"disabled@icloud.com"}, Date: 11},
	}
	incoming := []MailMessage{
		{UID: 10, Aliases: []string{"active@icloud.com"}, Subject: "updated", Date: 10},
		{UID: 12, Aliases: []string{"active@icloud.com"}, Date: 12},
	}
	merged := mergeMailMessages(existing, incoming, []string{"active@icloud.com"}, 5000)
	if len(merged) != 2 || merged[0].UID != 12 || merged[1].UID != 10 || merged[1].Subject != "updated" {
		t.Fatalf("unexpected incremental merge: %#v", merged)
	}
}

func TestWaitForRevisionWakesAfterCacheChange(t *testing.T) {
	service := NewMailboxService(t.TempDir(), nil)
	initial := mailboxCache{Status: MailboxStatus{UIDValidity: 7, MailboxGeneration: "generation-1", Revision: 3}, Messages: []MailMessage{{UID: 11, Aliases: []string{"one@icloud.com"}}}}
	if err := storage.WriteJSON(service.path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan MailboxStatus, 1)
	go func() { done <- service.WaitForRevision(3, 5*time.Second) }()
	time.Sleep(30 * time.Millisecond)
	if _, err := service.Hide("one@icloud.com", 11, 7, "generation-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case status := <-done:
		if status.Revision != 4 {
			t.Fatalf("unexpected revision: %#v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("revision waiter was not notified")
	}
}

func TestParseMailMessageDoesNotExposeUnrelatedMail(t *testing.T) {
	raw := "From: a@example.com\r\nTo: other@example.com\r\nSubject: code 123456\r\n\r\nhello"
	message, err := parseMailMessage(8, []byte(raw), map[string]bool{"demo@icloud.com": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.Aliases) != 0 {
		t.Fatalf("unrelated message matched: %#v", message)
	}
}

func TestParseMailMessageDecodesBase64Text(t *testing.T) {
	raw := "From: a@example.com\r\nTo: demo@icloud.com\r\nSubject: Code\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\nVmVyaWZpY2F0aW9uIGNvZGU6IDk5ODg3Nw=="
	message, err := parseMailMessage(9, []byte(raw), map[string]bool{"demo@icloud.com": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(message.Text, "998877") || len(message.Codes) != 1 || message.Codes[0] != "998877" {
		t.Fatalf("base64 body not decoded: %#v", message)
	}
}

func TestExtractPartnerCodeRequiresExactTemplate(t *testing.T) {
	code := "CnP9dEf2GhJ3KmN4"
	if got := extractPartnerCodes("Your partner code\n\n" + code); len(got) != 1 || got[0] != code {
		t.Fatalf("partner code not found: %#v", got)
	}
	if got := extractPartnerCodes("Affiliate code\n" + code); len(got) != 0 {
		t.Fatalf("loose partner code match: %#v", got)
	}
}

func TestHideBatchIsAtomicDeduplicatedAndOneRevision(t *testing.T) {
	service := NewMailboxService(t.TempDir(), nil)
	initial := mailboxCache{
		Status: MailboxStatus{UIDValidity: 7, MailboxGeneration: "generation-1", Revision: 3},
		Messages: []MailMessage{
			{UID: 11, Aliases: []string{"one@icloud.com", "copy@icloud.com"}},
			{UID: 22, Aliases: []string{"two@icloud.com"}},
		},
	}
	if err := storage.WriteJSON(service.path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	items := []struct {
		Alias string `json:"alias"`
		UID   uint32 `json:"uid"`
	}{
		{Alias: "one@icloud.com", UID: 11},
		{Alias: "copy@icloud.com", UID: 11},
		{Alias: "two@icloud.com", UID: 22},
	}

	result, err := service.HideBatch(items, 7, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if result["uniqueUIDCount"] != 2 || result["newlyHiddenCount"] != 2 || result["revision"] != int64(4) {
		t.Fatalf("unexpected result: %#v", result)
	}
	var saved mailboxCache
	if err := storage.ReadJSON(service.path, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Hidden) != 3 || saved.Status.Revision != 4 {
		t.Fatalf("unexpected cache after hide: %#v", saved)
	}

	repeated, err := service.HideBatch(items, 7, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	if repeated["newlyHiddenCount"] != 0 || repeated["alreadyHiddenCount"] != 2 || repeated["revision"] != int64(4) {
		t.Fatalf("batch replay was not idempotent: %#v", repeated)
	}
}

func TestHideBatchRejectsInvalidItemWithoutPartialWrite(t *testing.T) {
	service := NewMailboxService(t.TempDir(), nil)
	initial := mailboxCache{
		Status:   MailboxStatus{UIDValidity: 7, MailboxGeneration: "generation-1", Revision: 3},
		Messages: []MailMessage{{UID: 11, Aliases: []string{"one@icloud.com"}}},
	}
	if err := storage.WriteJSON(service.path, initial, 0o600); err != nil {
		t.Fatal(err)
	}
	items := []struct {
		Alias string `json:"alias"`
		UID   uint32 `json:"uid"`
	}{
		{Alias: "one@icloud.com", UID: 11},
		{Alias: "missing@icloud.com", UID: 99},
	}

	if _, err := service.HideBatch(items, 7, "generation-1"); err == nil {
		t.Fatal("expected invalid batch to fail")
	}
	var saved mailboxCache
	if err := storage.ReadJSON(service.path, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Hidden) != 0 || saved.Status.Revision != 3 {
		t.Fatalf("invalid batch partially changed cache: %#v", saved)
	}
}

func TestMailboxListsReturnPreviewButDetailKeepsFullText(t *testing.T) {
	service := NewMailboxService(t.TempDir(), nil)
	fullText := strings.Repeat("正文", 200)
	cache := mailboxCache{
		Status:   MailboxStatus{UIDValidity: 7, MailboxGeneration: "generation-1"},
		Messages: []MailMessage{{UID: 11, Aliases: []string{"one@icloud.com"}, Text: fullText}},
	}
	if err := storage.WriteJSON(service.path, cache, 0o600); err != nil {
		t.Fatal(err)
	}
	listed := service.Messages("one@icloud.com", 20)["messages"].([]MailMessage)
	if len(listed) != 1 || len(listed[0].Text) > 160 {
		t.Fatalf("list did not return a bounded preview: %#v", listed)
	}
	detail, ok := service.Message("one@icloud.com", 11)
	if !ok || detail.Text != fullText {
		t.Fatal("detail did not preserve the full cached body")
	}
}
