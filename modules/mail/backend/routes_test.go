package mail

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestLegacyAndCanonicalRoutesRequireAuthentication(t *testing.T) {
	root := t.TempDir()
	module := NewModule(root, "", "")
	mux := http.NewServeMux()
	module.RegisterRoutes(mux, httpx.APIKey("secret"))
	for _, path := range []string{"/v1/session/status", "/api/mail/v1/session/status"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s returned %d", path, response.Code)
		}
	}
}

func TestLegacyExportReturnsEnvelopeAndCanonicalReturnsCSV(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[{"hme":"a@icloud.com","anonymousId":"id"}]}}`))
	}))
	defer upstream.Close()
	session := NewSessionManager(configPath, filepath.Join(root, "state"))
	session.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(upstream.URL), WithHTTPClient(upstream.Client()))
	}
	api := &routeAPI{session: session, refresh: NewAutoRefresh(filepath.Join(root, "state"), session)}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/v1", true)
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	for path, expectedType := range map[string]string{"/v1/aliases/export.csv": "application/json", "/api/mail/v1/aliases/export.csv": "text/csv"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-API-Key", "secret")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !stringsContains(response.Header().Get("Content-Type"), expectedType) || !stringsContains(response.Body.String(), "a@icloud.com") {
			t.Fatalf("unexpected export response for %s: %d %s", path, response.Code, response.Body.String())
		}
	}
}

func TestShareSessionScopesMessageDetailsToLinkedAlias(t *testing.T) {
	root := t.TempDir()
	mailbox := NewMailboxService(root, nil)
	cache := mailboxCache{
		Status: MailboxStatus{Revision: 2, UIDValidity: 7, MailboxGeneration: "generation-1"},
		Messages: []MailMessage{
			{UID: 11, Aliases: []string{"demo@icloud.com"}, Subject: "demo"},
			{UID: 22, Aliases: []string{"private@icloud.com"}, Subject: "private"},
		},
	}
	if err := storage.WriteJSON(mailbox.path, cache, 0o600); err != nil {
		t.Fatal(err)
	}
	shares := NewShareLinkStore(root)
	created, err := shares.Create("demo@icloud.com", func() *int { value := 3600; return &value }())
	if err != nil {
		t.Fatal(err)
	}
	shareURL := created["shareUrl"].(string)
	token := shareURL[strings.LastIndex(shareURL, "#")+1:]

	api := &routeAPI{shares: shares, mailbox: mailbox}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	body, _ := json.Marshal(map[string]string{"token": token})
	sessionRequest := httptest.NewRequest(http.MethodPost, "/share/v1/session", bytes.NewReader(body))
	sessionResponse := httptest.NewRecorder()
	mux.ServeHTTP(sessionResponse, sessionRequest)
	if sessionResponse.Code != http.StatusOK {
		t.Fatalf("session exchange failed: %d %s", sessionResponse.Code, sessionResponse.Body.String())
	}
	cookies := sessionResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("missing share session cookie: %#v", cookies)
	}

	for path, expected := range map[string]int{
		"/share/v1/messages/11": http.StatusOK,
		"/share/v1/messages/22": http.StatusNotFound,
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookies[0])
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestShareWaitRejectsInvalidRevisionBeforePolling(t *testing.T) {
	root := t.TempDir()
	shares := NewShareLinkStore(root)
	created, err := shares.Create("demo@icloud.com", func() *int { value := 3600; return &value }())
	if err != nil {
		t.Fatal(err)
	}
	token := created["shareUrl"].(string)
	token = token[strings.LastIndex(token, "#")+1:]
	sessionToken, _, ok := shares.CreateSession(token, 3600)
	if !ok {
		t.Fatal("share session not created")
	}
	api := &routeAPI{shares: shares, mailbox: NewMailboxService(root, nil)}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	request := httptest.NewRequest(http.MethodGet, "/share/v1/sync/wait?revision=invalid&timeout=25", nil)
	request.AddCookie(&http.Cookie{Name: "hme_share_session", Value: sessionToken})
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision returned %d: %s", response.Code, response.Body.String())
	}
}
