package mail

import (
	"strings"
	"testing"
)

func TestShareLinkStoresOnlyDigestAndUsesSessionToken(t *testing.T) {
	store := NewShareLinkStore(t.TempDir())
	result, err := store.Create("demo@icloud.com", func() *int { value := 3600; return &value }())
	if err != nil {
		t.Fatal(err)
	}
	url, ok := result["shareUrl"].(string)
	if !ok || len(url) < 10 {
		t.Fatalf("missing share url: %#v", result)
	}
	token := url[strings.LastIndex(url, "#")+1:]
	session, link, ok := store.CreateSession(token, 3600)
	if !ok || session == "" || link.Alias != "demo@icloud.com" {
		t.Fatalf("session exchange failed")
	}
	if _, ok := store.ResolveSession(session); !ok {
		t.Fatal("session did not resolve")
	}
	if _, ok := store.ResolveSession(token); ok {
		t.Fatal("link token must not be accepted as session token")
	}
	if links := store.List("demo@icloud.com"); len(links) != 1 || links[0]["tokenHash"] != nil {
		t.Fatalf("sensitive token data leaked: %#v", links)
	}
}
