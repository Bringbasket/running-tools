package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
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

func TestMailActivityLogRecordsBusinessRoutesButNotLogQueries(t *testing.T) {
	root := t.TempDir()
	logs := activitylog.New("mail", filepath.Join(root, "logs"), nil)
	api := &routeAPI{
		session: NewSessionManager(filepath.Join(root, "missing.json"), filepath.Join(root, "state")),
		logs:    logs,
	}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)

	request := httptest.NewRequest(http.MethodGet, "/api/mail/v1/aliases", nil)
	request.Header.Set("X-API-Key", "secret")
	request = request.WithContext(contextWithRequestID(request.Context(), "request-log-test"))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("unexpected aliases response: %d %s", response.Code, response.Body.String())
	}

	page, err := logs.Query(request.Context(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Action != "alias.list" || page.Items[0].Outcome != "failure" || page.Items[0].RequestID != "request-log-test" || page.Items[0].Metadata["errorCode"] != "SESSION_MISSING" || page.Items[0].Detail == "" {
		t.Fatalf("unexpected activity log: %#v", page)
	}

	logRequest := httptest.NewRequest(http.MethodGet, "/api/mail/v1/activity-logs", nil)
	logRequest.Header.Set("X-API-Key", "secret")
	logResponse := httptest.NewRecorder()
	mux.ServeHTTP(logResponse, logRequest)
	if logResponse.Code != http.StatusOK {
		t.Fatalf("unexpected log query response: %d %s", logResponse.Code, logResponse.Body.String())
	}
	page, _ = logs.Query(request.Context(), activitylog.Query{Page: 1, PageSize: 10})
	if page.Total != 1 {
		t.Fatalf("log query generated another log: %#v", page)
	}
}

func TestMailActivityLogClearRouteDeletesRecords(t *testing.T) {
	root := t.TempDir()
	logs := activitylog.New("mail", filepath.Join(root, "logs"), nil)
	logs.Record(context.Background(), activitylog.Input{Category: "alias", Action: "alias.list", Summary: "列表", Source: "user"})
	api := &routeAPI{logs: logs}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	request := httptest.NewRequest(http.MethodPost, "/api/mail/v1/activity-logs/clear", strings.NewReader("{}"))
	request.Header.Set("X-API-Key", "secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected clear response: %d %s", response.Code, response.Body.String())
	}
	page, err := logs.Query(context.Background(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("clear did not remove records: page=%#v err=%v", page, err)
	}
}

func contextWithRequestID(ctx context.Context, requestID string) context.Context {
	request := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	request.Header.Set("X-Request-ID", requestID)
	var captured context.Context
	httpx.RequestIDs(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { captured = r.Context() })).ServeHTTP(httptest.NewRecorder(), request)
	return captured
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
			{UID: 11, Aliases: []string{"demo@icloud.com"}, Subject: "verification code", Text: "line one\r\n\r\ncode: 123456", SafeHTML: "<p>private</p>"},
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
	token := shareTokenFromURL(shareURL)

	api := &routeAPI{shares: shares, mailbox: mailbox}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	body, _ := json.Marshal(map[string]string{"token": token})
	latestRequest := httptest.NewRequest(http.MethodGet, "/share/v1/latest?token="+url.QueryEscape(token), nil)
	latestResponse := httptest.NewRecorder()
	mux.ServeHTTP(latestResponse, latestRequest)
	latestBody := latestResponse.Body.String()
	if latestResponse.Code != http.StatusOK || !strings.Contains(latestBody, "demo") {
		t.Fatalf("latest JSON endpoint failed: %d %s", latestResponse.Code, latestResponse.Body.String())
	}
	if strings.Contains(latestBody, "safeHtml") || strings.Contains(latestBody, `\r\n`) || !strings.Contains(latestBody, `"codes":["123456"]`) || !strings.Contains(latestBody, `"text":"line one code: 123456"`) {
		t.Fatalf("latest JSON was not compacted: %s", latestBody)
	}
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
	token = shareTokenFromURL(token)
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
