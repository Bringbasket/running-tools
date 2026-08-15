package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

type rewriteAppleTransport struct {
	target *url.URL
}

func (transport rewriteAppleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.URL.Scheme = transport.target.Scheme
	clone.URL.Host = transport.target.Host
	clone.Host = transport.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func appleTestHTTPClient(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	return &http.Client{Transport: rewriteAppleTransport{target: target}, Timeout: time.Second}
}

func healthyAppleAccountState() AppleAccountState {
	return AppleAccountState{
		AppleID:         "owner@example.com",
		Host:            "appleid.apple.com",
		Origin:          "https://account.apple.com",
		Cookies:         []AppleSessionCookie{{Name: "session", Value: "secret", Domain: "appleid.apple.com", Path: "/"}},
		Scnt:            "secret-scnt",
		SessionID:       "secret-session",
		APIKey:          "secret-api-key",
		ManageExpiresAt: time.Now().Add(time.Hour),
		LastCheckedAt:   time.Now(),
		LastCheckOK:     true,
	}
}

func TestAppleLoginResultNeverSerializesProtocolSecrets(t *testing.T) {
	result := AppleLoginStartResult{
		Channel:      AppleChannelAccount,
		Message:      "ok",
		accountState: func() *AppleAccountState { value := healthyAppleAccountState(); return &value }(),
		webConfig:    &ICloudConfig{Cookie: "secret-cookie"},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"secret-cookie", "secret-scnt", "secret-session", "secret-api-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("login response leaked %q: %s", secret, text)
		}
	}
}

func TestAppleAccountPersistenceStatusDoesNotExposeSecrets(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	state := healthyAppleAccountState()
	if err := manager.persistAppleLoginResult(AppleLoginStartResult{Channel: AppleChannelAccount, accountState: &state}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(manager.Status())
	text := string(data)
	for _, secret := range []string{"secret", "secret-scnt", "secret-api-key"} {
		if strings.Contains(text, secret) {
			t.Fatalf("status leaked %q: %s", secret, text)
		}
	}
	if !manager.Status().AppleLogin.AppleAccount.Healthy {
		t.Fatalf("persisted account state not reported healthy: %#v", manager.Status().AppleLogin)
	}
}

func TestCreateAliasDoesNotFallbackAfterConfirmationStarts(t *testing.T) {
	completeRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/account/manage/email/private/add":
			_, _ = w.Write([]byte(`{"emailAddress":"generated@icloud.com"}`))
		case "/account/manage/email/private/add/complete":
			completeRequests++
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"private":"must-not-be-exposed"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "missing-config.json"), filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	if err := storage.WriteJSON(manager.appleAccountPath, healthyAppleAccountState(), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := manager.CreateAlias(context.Background(), "shopping", "")
	var protocol *AppleProtocolError
	if !errors.As(err, &protocol) || !protocol.MayHaveCreated {
		t.Fatalf("expected uncertain Apple Account error, got %T %v", err, err)
	}
	if strings.Contains(err.Error(), "must-not-be-exposed") {
		t.Fatalf("upstream body leaked: %v", err)
	}
	if completeRequests != 1 {
		t.Fatalf("confirmation was retried %d times", completeRequests)
	}
}

func TestAppleAccountCreateRefreshesBeforeConfirmationAndConfirmsDetails(t *testing.T) {
	addRequests, completeRequests, detailRequests := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/account/manage/gs/ws/token":
			_, _ = w.Write([]byte(`{"timeOutInterval":30}`))
		case "/account/manage":
			_, _ = w.Write([]byte(`{"apiKey":"refreshed-api-key"}`))
		case "/account/manage/forwardemail":
			_, _ = w.Write([]byte(`{"forwardToEmail":"owner@example.com"}`))
		case "/account/manage/email/private/add":
			addRequests++
			if addRequests == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"reason":"Invalid session"}`))
				return
			}
			_, _ = w.Write([]byte(`{"emailAddress":"generated@icloud.com"}`))
		case "/account/manage/email/private/add/complete":
			completeRequests++
			_, _ = w.Write([]byte(`{"id":"alias-id","emailAddress":"generated@icloud.com","label":"shopping","active":true}`))
		case "/account/manage/email/private/alias-id.em":
			detailRequests++
			_, _ = w.Write([]byte(`{"id":"alias-id","emailAddress":"generated@icloud.com","label":"shopping","forwardToEmail":"owner@example.com","active":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	alias, state, err := client.CreateWithAppleAccount(context.Background(), healthyAppleAccountState(), "shopping", "")
	if err != nil {
		t.Fatal(err)
	}
	if addRequests != 2 || completeRequests != 1 || detailRequests != 1 {
		t.Fatalf("unexpected request counts: add=%d complete=%d detail=%d", addRequests, completeRequests, detailRequests)
	}
	if state.APIKey != "refreshed-api-key" || alias["forwardToEmail"] != "owner@example.com" || alias["detailConfirmed"] != true {
		t.Fatalf("refresh or detail confirmation missing: state=%#v alias=%#v", state, alias)
	}
}

func TestCreateChannelCooldownSurvivesRestartAndSkipsAppleAccount(t *testing.T) {
	appleRequests := 0
	appleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		appleRequests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"errorMessage":"reached the limit","retryAfter":600}}`))
	}))
	defer appleServer.Close()
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/hme/generate" {
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":"fallback@icloud.com"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"hme":{"anonymousId":"old-id","hme":"fallback@icloud.com","label":"shopping"}}}`))
	}))
	defer webServer.Close()

	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSON(filepath.Join(stateDir, "apple-account-state.json"), healthyAppleAccountState(), 0o600); err != nil {
		t.Fatal(err)
	}
	newManager := func() *SessionManager {
		manager := NewSessionManager(configPath, stateDir)
		manager.appleAuth.httpClient = appleTestHTTPClient(appleServer)
		manager.newClient = func(config ICloudConfig) (*Client, error) {
			return NewClient(config, WithBaseURL(webServer.URL), WithHTTPClient(webServer.Client()))
		}
		return manager
	}
	first, err := newManager().CreateAlias(context.Background(), "shopping", "")
	if err != nil || first["fallbackUsed"] != true {
		t.Fatalf("first fallback failed: %#v %v", first, err)
	}
	secondManager := newManager()
	second, err := secondManager.CreateAlias(context.Background(), "shopping", "")
	if err != nil || second["usedChannel"] != AppleChannelICloudWeb || second["fallbackUsed"] != false {
		t.Fatalf("persisted cooldown was not used: %#v %v", second, err)
	}
	if appleRequests != 1 || secondManager.Status().AppleLogin.AppleAccount.CooldownRemaining < 500 {
		t.Fatalf("unexpected cooldown state: requests=%d status=%#v", appleRequests, secondManager.Status().AppleLogin.AppleAccount)
	}
}

func TestAppleAccountRecognizesLimitInSuccessfulHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":false,"error":{"errorCode":"-41015","errorMessage":"reached the limit","retryAfter":17}}`))
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	_, _, err := client.CreateWithAppleAccount(context.Background(), healthyAppleAccountState(), "shopping", "")
	var protocol *AppleProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "APPLE_ACCOUNT_LIMIT" || protocol.RetryAfter != 17*time.Second {
		t.Fatalf("unexpected limit error: %#v", err)
	}
}

func TestCreateAliasFallsBackBeforeConfirmationStarts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	if err := storage.WriteJSON(manager.appleAccountPath, healthyAppleAccountState(), 0o600); err != nil {
		t.Fatal(err)
	}
	oldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/hme/generate":
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":"fallback@icloud.com"}}`))
		case "/v1/hme/reserve":
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":{"anonymousId":"old-id","hme":"fallback@icloud.com","label":"shopping"}}}`))
		}
	}))
	defer oldServer.Close()
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(oldServer.URL), WithHTTPClient(oldServer.Client()))
	}
	alias, err := manager.CreateAlias(context.Background(), "shopping", "")
	if err != nil || alias["hme"] != "fallback@icloud.com" {
		t.Fatalf("safe fallback failed: %#v %v", alias, err)
	}
}

func TestPersistAppleAccountRejectsDifferentCurrentAccount(t *testing.T) {
	root := t.TempDir()
	config := testConfig()
	config.AppleID = "first@example.com"
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	state := healthyAppleAccountState()
	if err := manager.persistAppleLoginResult(AppleLoginStartResult{accountState: &state}); err == nil || !strings.Contains(err.Error(), "不是同一账号") {
		t.Fatalf("account mismatch was accepted: %v", err)
	}
}

func TestAppleWebEndpointsFollowAccountCountry(t *testing.T) {
	if endpoint := appleWebEndpointsForCountry("CHN"); endpoint.Host != "www.icloud.com.cn" || !strings.Contains(endpoint.Auth, "apple.com.cn") {
		t.Fatalf("unexpected China endpoint: %#v", endpoint)
	}
	if endpoint := appleWebEndpointsForCountry("USA"); endpoint.Host != "www.icloud.com" || strings.Contains(endpoint.Auth, ".cn") {
		t.Fatalf("unexpected international endpoint: %#v", endpoint)
	}
}

func TestAppleHashcashSatisfiesRequestedBits(t *testing.T) {
	value, err := generateAppleHashcash(8, "challenge", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "1:8:") {
		t.Fatalf("unexpected hashcash: %s", value)
	}
}

func TestAppleFingerprintIsCompressedAndDynamic(t *testing.T) {
	first := appleCompressedFingerprint(time.Unix(1786500000, 0))
	second := appleCompressedFingerprint(time.Unix(1786500001, 0))
	if first == second || strings.Contains(first, "TF1") || len(first) < 10 {
		t.Fatalf("unexpected fingerprints: %q %q", first, second)
	}
}

func TestApplePhoneNormalizationKeepsOnlyProtocolFields(t *testing.T) {
	phone, ok := normalizeApplePhone(map[string]any{"id": float64(2), "nonFTEU": true, "numberWithDialCode": "+86 138****0000"})
	if !ok || phone["id"] != float64(2) || phone["nonFTEU"] != true || len(phone) != 2 {
		t.Fatalf("unexpected phone payload: %#v", phone)
	}
}

func TestAppleVerifyRejectsNonNumericCodeBeforeNetwork(t *testing.T) {
	client := NewAppleAuthClient()
	_, err := client.Verify(context.Background(), "missing", "12ab56")
	var protocol *AppleProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "INVALID_2FA_CODE" {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestAppleAccountPrimeOmitsPortalScntFromManageToken(t *testing.T) {
	var tokenScnt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/account/manage/section/privacy":
			w.Header().Set("scnt", "page-scnt")
			_, _ = w.Write([]byte("<html></html>"))
		case "/bootstrap/portal":
			_, _ = w.Write([]byte(`{"timeOutInterval":15}`))
		case "/account/manage/gs/ws/token":
			tokenScnt = request.Header.Get("scnt")
			w.Header().Set("scnt", "manage-scnt")
			_, _ = w.Write([]byte(`{"timeOutInterval":15}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	session := &appleAuthSession{Channel: AppleChannelAccount, Endpoints: appleAccountAuthEndpoints(), UserAgent: appleAccountUserAgent}
	if err := client.primeAppleAccount(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if tokenScnt != "" {
		t.Fatalf("pre-login manage token received portal scnt %q", tokenScnt)
	}
	if session.ManageScnt != "manage-scnt" {
		t.Fatalf("manage scnt = %q, want response scnt", session.ManageScnt)
	}
}

func TestAppleAccountHTTPErrorIncludesSafeAppleMessage(t *testing.T) {
	err := appleAccountHTTPErrorAt(http.StatusInternalServerError, []byte(`{"reason":"Invalid global session","error":{"errorCode":"2"}}`), false, "/account/manage/gs/ws/token")
	var protocol *AppleProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("error type = %T, want AppleProtocolError", err)
	}
	if protocol.Code != "APPLE_ACCOUNT_EXPIRED" || !strings.Contains(protocol.Message, "已失效") {
		t.Fatalf("unexpected diagnostic: %#v", protocol)
	}
	if strings.Contains(protocol.Message, "errorCode") {
		t.Fatalf("raw error envelope leaked: %s", protocol.Message)
	}
}

func TestAppleAccountSRPHeadersIncludeSessionToken(t *testing.T) {
	session := &appleAuthSession{
		Channel:      AppleChannelAccount,
		Endpoints:    appleAccountAuthEndpoints(),
		ClientID:     appleManageOAuthClientID,
		FrameID:      "frame",
		UserAgent:    appleAccountUserAgent,
		SessionToken: "session-token",
	}
	if got := session.srpHeaders()["X-Apple-Session-Token"]; got != "session-token" {
		t.Fatalf("X-Apple-Session-Token = %q", got)
	}
}

func TestAppleAccountListAndManagementEndpoints(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests = append(requests, request.Method+" "+request.URL.Path)
		if request.Header.Get("X-Apple-Api-Key") != "secret-api-key" {
			t.Fatalf("missing API key on %s", request.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /account/manage/email/private":
			_, _ = w.Write([]byte(`{"forwardToEmailAddress":"owner@example.com","privateEmailList":[{"id":"active-id","emailAddress":"active@icloud.com","label":"shopping","active":true,"createdDate":"2026-08-13T10:00:00Z"}],"inactivePrivateEmailList":[{"id":"inactive-id","emailAddress":"off@icloud.com","active":false}]}`))
		case "POST /account/manage/email/private/active-id/note", "DELETE /account/manage/email/private/active-id/stop", "POST /account/manage/email/private/inactive-id/reactivate", "DELETE /account/manage/email/private/inactive-id/remove":
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	aliases, state, err := client.ListWithAppleAccount(context.Background(), state)
	if err != nil || len(aliases) != 2 {
		t.Fatalf("list failed: aliases=%#v err=%v", aliases, err)
	}
	if aliases[0]["hme"] != "active@icloud.com" || aliases[0]["forwardToEmail"] != "owner@example.com" || aliases[1]["isActive"] != false {
		t.Fatalf("unexpected normalized aliases: %#v", aliases)
	}
	if _, state, err = client.UpdateWithAppleAccount(context.Background(), state, "active-id", "new", "note"); err != nil {
		t.Fatal(err)
	}
	if _, state, err = client.SetActiveWithAppleAccount(context.Background(), state, "active-id", false); err != nil {
		t.Fatal(err)
	}
	if _, state, err = client.SetActiveWithAppleAccount(context.Background(), state, "inactive-id", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err = client.DeleteWithAppleAccount(context.Background(), state, "inactive-id"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"GET /account/manage/email/private",
		"POST /account/manage/email/private/active-id/note",
		"DELETE /account/manage/email/private/active-id/stop",
		"POST /account/manage/email/private/inactive-id/reactivate",
		"DELETE /account/manage/email/private/inactive-id/remove",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestSessionListAliasesUsesAppleAccountWithoutICloudWeb(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/account/manage/email/private" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"forwardToEmailAddress":"owner@example.com","privateEmailList":[{"id":"alias-id","emailAddress":"only-account@icloud.com","active":true}]}`))
	}))
	defer server.Close()
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "missing-config.json"), filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	if err := storage.WriteJSON(manager.appleAccountPath, healthyAppleAccountState(), 0o600); err != nil {
		t.Fatal(err)
	}
	aliases, source, err := manager.listAliases(context.Background())
	if err != nil || source != AppleChannelAccount || len(aliases) != 1 || aliases[0]["hme"] != "only-account@icloud.com" {
		t.Fatalf("unexpected list: source=%s aliases=%#v err=%v", source, aliases, err)
	}
}

func TestAppleAccountRequestRetriesServerErrorsAndCommitsOnlySuccess(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 3 {
			http.SetCookie(w, &http.Cookie{Name: "rotated", Value: "failed", Path: "/"})
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"reason":"temporary upstream failure"}`))
			return
		}
		w.Header().Set("scnt", "fresh-scnt")
		_, _ = w.Write([]byte(`{"timeOutInterval":15}`))
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	before := cloneAppleAccountState(&state)
	var token struct {
		TimeOutInterval int `json:"timeOutInterval"`
	}
	if _, err := client.appleAccountRequest(context.Background(), &state, http.MethodGet, "/account/manage/gs/ws/token", "", nil, &token); err != nil {
		t.Fatal(err)
	}
	if requests != appleRequestMaxAttempts || token.TimeOutInterval != 15 {
		t.Fatalf("unexpected retry result: requests=%d token=%#v", requests, token)
	}
	if state.Scnt != "fresh-scnt" || reflect.DeepEqual(state, before) {
		t.Fatalf("successful response did not commit expected state: %#v", state)
	}
	if len(state.Cookies) != len(before.Cookies) || state.Cookies[0].Value != before.Cookies[0].Value {
		t.Fatalf("failed response cookie leaked into state: %#v", state.Cookies)
	}
}

func TestAppleAccountAuthResponseIsNotRetriedOrCommitted(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.SetCookie(w, &http.Cookie{Name: "rotated", Value: "failed", Path: "/"})
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"reason":"Invalid global session"}`))
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	before := cloneAppleAccountState(&state)
	_, err := client.appleAccountRequest(context.Background(), &state, http.MethodGet, "/account/manage/gs/ws/token", "", nil, nil)
	if err == nil || requests != 1 {
		t.Fatalf("authentication response was retried: requests=%d err=%v", requests, err)
	}
	if !reflect.DeepEqual(state, before) {
		t.Fatalf("authentication failure contaminated state: before=%#v after=%#v", before, state)
	}
}

func TestAppleAccountGenericForbiddenIsNotTreatedAsExpired(t *testing.T) {
	err := appleAccountHTTPErrorAt(http.StatusForbidden, []byte(`{"reason":"Forbidden"}`), false, "/account/manage")
	var protocol *AppleProtocolError
	if !errors.As(err, &protocol) {
		t.Fatalf("error type = %T, want AppleProtocolError", err)
	}
	if protocol.Code == "APPLE_ACCOUNT_EXPIRED" || !protocol.Retryable {
		t.Fatalf("generic forbidden response was treated as auth expiry: %#v", protocol)
	}
}

func TestAppleAccountNoTTLWarmsAndRetriesWithoutScnt(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path+" scnt="+r.Header.Get("scnt"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/account/manage/gs/ws/token":
			if r.Header.Get("scnt") == "" {
				w.Header().Set("scnt", "refreshed-scnt")
				_, _ = w.Write([]byte(`{"timeOutInterval":12}`))
			} else {
				_, _ = w.Write([]byte(`{}`))
			}
		case "/account/manage/section/privacy":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
		case "/bootstrap/portal":
			_, _ = w.Write([]byte(`{"timeOutInterval":12}`))
		case "/account/manage":
			_, _ = w.Write([]byte(`{"apiKey":"refreshed-api-key"}`))
		case "/account/manage/forwardemail":
			_, _ = w.Write([]byte(`{"forwardToEmail":"owner@example.com"}`))
		case "/v2/jslogs":
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	state.ManageExpiresAt = time.Now().Add(-time.Minute)
	if err := client.refreshAppleAccountState(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if state.APIKey != "refreshed-api-key" || state.Scnt != "refreshed-scnt" || !state.LastCheckOK {
		t.Fatalf("state was not refreshed: %#v", state)
	}
	if !state.ManageExpiresAt.After(time.Now()) {
		t.Fatalf("expired TTL was not replaced after successful verification: %v", state.ManageExpiresAt)
	}
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "GET /account/manage/section/privacy") || !strings.Contains(joined, "GET /bootstrap/portal") || !strings.Contains(joined, "GET /account/manage/gs/ws/token scnt=") || !strings.Contains(joined, "POST /v2/jslogs") {
		t.Fatalf("missing TTL recovery sequence: %s", joined)
	}
}

func TestAppleAccountJSLogsFailureDoesNotFailHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/account/manage/gs/ws/token":
			_, _ = w.Write([]byte(`{"timeOutInterval":15}`))
		case "/account/manage":
			_, _ = w.Write([]byte(`{"apiKey":"refreshed-api-key"}`))
		case "/account/manage/forwardemail":
			_, _ = w.Write([]byte(`{"forwardToEmail":"owner@example.com"}`))
		case "/v2/jslogs":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"reason":"logs unavailable"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	if err := client.refreshAppleAccountState(context.Background(), &state); err != nil {
		t.Fatal(err)
	}
	if !state.LastCheckOK || state.APIKey != "refreshed-api-key" {
		t.Fatalf("jslogs failure changed health result: %#v", state)
	}
}

func TestAppleAccountManagementFailureMarksReauthWithoutRotatingState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "rotated", Value: "bad", Path: "/"})
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"reason":"Invalid global session"}`))
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	original := cloneAppleAccountState(&state)
	_, updated, err := client.ListWithAppleAccount(context.Background(), state)
	if err == nil || !appleAccountRequiresReauth(err) {
		t.Fatalf("expected re-authentication error, got %v", err)
	}
	if !updated.requiresReauth() || updated.LastCheckOK {
		t.Fatalf("management failure did not persist reauth state: %#v", updated)
	}
	if !reflect.DeepEqual(updated.Cookies, original.Cookies) || updated.Scnt != original.Scnt || updated.APIKey != original.APIKey {
		t.Fatalf("failed response rotated persisted credentials: before=%#v after=%#v", original, updated)
	}
}

func TestAppleAccountMutationFailureIsNotRetried(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"reason":"temporary upstream failure"}`))
	}))
	defer server.Close()
	client := NewAppleAuthClient()
	client.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	_, _, err := client.UpdateWithAppleAccount(context.Background(), state, "alias-id", "shopping", "")
	if err == nil || requests != 1 {
		t.Fatalf("mutating request was replayed: requests=%d err=%v", requests, err)
	}
}
