package mail

import (
	"net/url"
	"strings"
	"testing"
)

func shareTokenFromURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if token := parsed.Query().Get("token"); token != "" {
		return token
	}
	return strings.TrimSpace(parsed.Fragment)
}

func TestShareLinkStoresOnlyDigestAndUsesSessionToken(t *testing.T) {
	store := NewShareLinkStore(t.TempDir())
	result, err := store.Create("demo@icloud.com", func() *int { value := 3600; return &value }())
	if err != nil {
		t.Fatal(err)
	}
	shareURL, ok := result["shareUrl"].(string)
	if !ok || len(shareURL) < 10 {
		t.Fatalf("missing share url: %#v", result)
	}
	parsed, err := url.Parse(shareURL)
	if err != nil || parsed.Path != "/mail" || parsed.Query().Get("email") != "demo@icloud.com" || parsed.Query().Get("token") == "" {
		t.Fatalf("share url must be a direct JSON link, got %q", shareURL)
	}
	token := shareTokenFromURL(shareURL)
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

func TestShareLinkBatchCreatesAllLinksAtomically(t *testing.T) {
	store := NewShareLinkStore(t.TempDir())
	created, err := store.CreateBatch([]shareLinkCreateInput{
		{Alias: "first@icloud.com", AliasCreatedAt: 100},
		{Alias: "second@icloud.com", AliasCreatedAt: 200},
	}, func() *int { value := 3600; return &value }())
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 {
		t.Fatalf("created %d links, want 2", len(created))
	}
	if created[0]["alias"] != "first@icloud.com" || created[1]["alias"] != "second@icloud.com" {
		t.Fatalf("unexpected aliases: %#v", created)
	}
	if created[0]["shareUrl"] == created[1]["shareUrl"] {
		t.Fatal("batch links must have unique tokens")
	}
	if len(store.List("first@icloud.com")) != 1 || len(store.List("second@icloud.com")) != 1 {
		t.Fatal("batch links were not persisted")
	}
	if store.List("first@icloud.com")[0]["tokenHash"] != nil {
		t.Fatal("token digest leaked from list response")
	}
}
