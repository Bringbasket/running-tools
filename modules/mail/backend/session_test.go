package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestSessionImportPersistsSecretsButStatusDoesNotExposeThem(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "hme-config.json"), filepath.Join(root, "state"))
	result, err := manager.Import(validCurl, RegionInternational)
	if err != nil {
		t.Fatal(err)
	}
	if result["imported"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}
	raw, _ := json.Marshal(manager.Status())
	if strings.Contains(string(raw), "X-APPLE-") || strings.Contains(string(raw), `\"user\"`) || strings.Contains(string(raw), `\"token\"`) {
		t.Fatalf("status leaked a cookie value: %s", raw)
	}
	config, err := LoadICloudConfig(filepath.Join(root, "hme-config.json"))
	if err != nil || !strings.Contains(config.Cookie, "X-APPLE-DS-WEB-SESSION-TOKEN") {
		t.Fatalf("session was not persisted: %v", err)
	}
}

func TestSessionCheckRecordsValidState(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "hme-config.json")
	config := testConfig()
	if err := storage.WriteJSON(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[{"hme":"a@icloud.com"}],"selectedForwardTo":"me@example.com"}}`))
	}))
	defer server.Close()
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	}
	status := manager.Check(context.Background())
	if !status.SessionValid || status.NeedsReauth || status.HME["aliasCount"] != 1 {
		t.Fatalf("unexpected status: %#v", status)
	}
	statusReloaded := NewSessionManager(configPath, filepath.Join(root, "state")).Status()
	aliasCount, ok := statusReloaded.HME["aliasCount"].(float64)
	if !ok || aliasCount != 1 || statusReloaded.HME["selectedForwardTo"] != "me@example.com" {
		t.Fatalf("HME summary was not persisted: %#v", statusReloaded)
	}
}

func TestSessionCheckMarksUnauthorizedSessionForReimport(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "hme-config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"reason":"Invalid global session"}`))
	}))
	defer server.Close()
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	}
	status := manager.Check(context.Background())
	if !status.NeedsReauth || status.SessionValid || status.LastError == nil || strings.HasPrefix(*status.LastError, `"`) {
		t.Fatalf("unexpected expired status: %#v", status)
	}
}

func TestSessionCheckPersistsRolledCookieOnUnauthorizedResponse(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "hme-config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "renewed", Path: "/"})
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"reason":"Invalid global session"}`))
	}))
	defer server.Close()
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	}
	status := manager.Check(context.Background())
	if !status.NeedsReauth {
		t.Fatalf("unexpected status: %#v", status)
	}
	stored, err := LoadICloudConfig(configPath)
	if err != nil || !strings.Contains(stored.Cookie, "SESSION=renewed") {
		t.Fatalf("rolled cookie was not persisted: %q %v", stored.Cookie, err)
	}
}

func TestSessionCheckDoesNotRefreshHealthyAppleAccountBeforeTTL(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	appleRequests := 0
	appleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appleRequests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer appleServer.Close()
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[]}}`))
	}))
	defer webServer.Close()
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(appleServer)
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(webServer.URL), WithHTTPClient(webServer.Client()))
	}
	state := healthyAppleAccountState()
	state.HealthState = AppleAccountStateHealthy
	state.ManageExpiresAt = time.Now().Add(30 * time.Minute)
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	status := manager.Check(context.Background())
	if !status.SessionValid || appleRequests != 0 {
		t.Fatalf("healthy Apple Account was refreshed before TTL: status=%#v requests=%d", status, appleRequests)
	}
}

func TestAutoRefreshClampsMinimumInterval(t *testing.T) {
	root := t.TempDir()
	service := NewAutoRefresh(root, NewSessionManager(filepath.Join(root, "config.json"), root))
	enabled := true
	interval := 60
	config, err := service.Update(&enabled, &interval)
	if err != nil {
		t.Fatal(err)
	}
	if config.IntervalSeconds != minimumRefreshInterval {
		t.Fatalf("interval not clamped: %d", config.IntervalSeconds)
	}
}

func TestAutoRefreshUsesAdaptiveWakeDelay(t *testing.T) {
	root := t.TempDir()
	service := NewAutoRefresh(root, NewSessionManager(filepath.Join(root, "config.json"), root))
	disabled := false
	if _, err := service.Update(&disabled, nil); err != nil {
		t.Fatal(err)
	}
	if delay := service.nextWakeDelay(); delay != autoRefreshIdlePoll {
		t.Fatalf("disabled delay = %s, want %s", delay, autoRefreshIdlePoll)
	}
	enabled := true
	if _, err := service.Update(&enabled, nil); err != nil {
		t.Fatal(err)
	}
	if delay := service.nextWakeDelay(); delay != autoRefreshMinimumWake {
		t.Fatalf("first-run delay = %s, want %s", delay, autoRefreshMinimumWake)
	}
	now := unixNow()
	if err := storage.WriteJSON(filepath.Join(root, "auto-refresh.json"), AutoRefreshConfig{
		Enabled: true, IntervalSeconds: minimumRefreshInterval, LastRunAt: &now,
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	if delay := service.nextWakeDelay(); delay != autoRefreshIdlePoll {
		t.Fatalf("fallback delay = %s, want %s", delay, autoRefreshIdlePoll)
	}
}

func TestAutoRefreshDoesNotDisableOnTransientAppleAccountFailure(t *testing.T) {
	status := SessionStatus{
		SessionState: SessionState{NeedsReauth: true},
		AppleLogin: AppleLoginStatus{AppleAccount: AppleChannelStatus{
			Configured: true,
			Healthy:    false,
			State:      AppleAccountStateDegraded,
		}},
	}
	if shouldDisableAutoRefresh(status) {
		t.Fatal("transient Apple Account degradation disabled the recovery worker")
	}
	status.AppleLogin.AppleAccount.RequiresReauth = true
	if !shouldDisableAutoRefresh(status) {
		t.Fatal("explicit Apple Account re-auth requirement did not disable the worker")
	}
}

func TestAutoRefreshUsesAppleAccountExpiryBeforeConfiguredInterval(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), root)
	now := time.Now()
	state := healthyAppleAccountState()
	state.LastCheckedAt = now
	state.ManageExpiresAt = now.Add(4 * time.Minute)
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewAutoRefresh(root, manager)
	enabled := true
	interval := 10 * 60
	if _, err := service.Update(&enabled, &interval); err != nil {
		t.Fatal(err)
	}
	status := service.Status()
	if status.NextRunAt == nil || *status.NextRunAt >= float64(now.Add(10*time.Minute).Unix()) {
		t.Fatalf("configured interval was not shortened for Apple Account expiry: %#v", status)
	}
	if status.RemainingSeconds == nil || *status.RemainingSeconds <= 0 {
		t.Fatalf("unexpected Apple Account refresh countdown: %#v", status)
	}
}

func TestAutoRefreshDoesNotWaitAfterAppleDeadlineWithPreviousRun(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), root)
	now := time.Now()
	state := healthyAppleAccountState()
	state.LastCheckedAt = now.Add(-10 * time.Minute)
	state.ManageExpiresAt = now.Add(-time.Minute)
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	lastRun := float64(now.UnixNano()) / float64(time.Second)
	if err := storage.WriteJSON(filepath.Join(root, "auto-refresh.json"), AutoRefreshConfig{
		Enabled: true, IntervalSeconds: 10 * 60, LastRunAt: &lastRun,
	}, 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewAutoRefresh(root, manager)
	status := service.Status()
	if status.NextRunAt == nil || *status.NextRunAt > float64(time.Now().Add(2*time.Second).UnixNano())/float64(time.Second) {
		t.Fatalf("expired Apple deadline was delayed by configured interval: %#v", status)
	}
}

func TestAppleAccountTransientKeepAlivePreservesHealthyState(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "rotated", Value: "must-not-commit", Path: "/"})
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"reason":"upstream temporarily unavailable"}`))
	}))
	defer server.Close()
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	original := healthyAppleAccountState()
	if err := storage.WriteJSON(manager.appleAccountPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := manager.KeepAliveAppleAccount(context.Background())
	if err == nil {
		t.Fatal("transient Apple failure unexpectedly succeeded")
	}
	var stored AppleAccountState
	if err := storage.ReadJSON(manager.appleAccountPath, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.LastCheckOK || stored.HealthState != AppleAccountStateDegraded {
		t.Fatalf("transient failure incorrectly invalidated account: %#v", stored)
	}
	if len(stored.Cookies) != len(original.Cookies) || stored.Cookies[0].Value != original.Cookies[0].Value {
		t.Fatalf("failed response contaminated persisted cookies: %#v", stored.Cookies)
	}
}

func TestAppleAccountAuthFailureRequiresRelogin(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"reason":"Invalid global session"}`))
	}))
	defer server.Close()
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	state := healthyAppleAccountState()
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	err := manager.KeepAliveAppleAccount(context.Background())
	if err == nil || !appleAccountRequiresReauth(err) {
		t.Fatalf("expected explicit authentication failure, got %v", err)
	}
	var stored AppleAccountState
	if err := storage.ReadJSON(manager.appleAccountPath, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.LastCheckOK || !stored.requiresReauth() {
		t.Fatalf("authentication failure was not persisted as reauth-required: %#v", stored)
	}
}

func TestAppleAccountTransientFailureSchedulesBoundedNextAttempt(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	now := time.Now()
	state := healthyAppleAccountState()
	state.LastCheckedAt = now.Add(-10 * time.Minute)
	state.ManageExpiresAt = now.Add(-time.Minute)
	state.LastAttemptAt = now
	state.HealthState = AppleAccountStateDegraded
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	anchor, deadline, ok := manager.appleAccountKeepAliveSchedule(now)
	if !ok || !anchor.Equal(state.LastAttemptAt) || deadline.Before(now.Add(2*time.Minute)) {
		t.Fatalf("transient failure was scheduled too aggressively: anchor=%v deadline=%v", anchor, deadline)
	}
}

func TestAppleAccountDegradedStateDoesNotSelectCreateChannel(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	config := testConfig()
	if err := storage.WriteJSON(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	state := healthyAppleAccountState()
	state.HealthState = AppleAccountStateDegraded
	if err := storage.WriteJSON(filepath.Join(root, "state", "apple-account-state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	status := manager.Status()
	if status.AppleLogin.AppleAccount.Healthy || status.AppleLogin.AppleAccount.State != AppleAccountStateDegraded {
		t.Fatalf("degraded account was exposed as healthy: %#v", status.AppleLogin.AppleAccount)
	}
	if status.AppleLogin.CreateChannel != AppleChannelICloudWeb {
		t.Fatalf("degraded account incorrectly selected for creation: %#v", status.AppleLogin)
	}
	state.HealthState = AppleAccountStateReauthRequired
	state.LastCheckOK = true // exercise compatibility with stale persisted flags
	if err := storage.WriteJSON(filepath.Join(root, "state", "apple-account-state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	status = manager.Status()
	if status.AppleLogin.AppleAccount.Healthy || !status.AppleLogin.AppleAccount.RequiresReauth || status.AppleLogin.CreateChannel == AppleChannelAccount {
		t.Fatalf("reauth-required account was exposed as healthy: %#v", status.AppleLogin)
	}
}

func TestLegacyAppleAccountStateMapsHealthyWhenStillValid(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	state := healthyAppleAccountState()
	state.HealthState = "" // state files written before healthState was added
	if err := storage.WriteJSON(filepath.Join(root, "state", "apple-account-state.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	status := manager.Status()
	account := status.AppleLogin.AppleAccount
	if !account.Healthy || account.State != AppleAccountStateHealthy {
		t.Fatalf("legacy healthy state was mapped inconsistently: %#v", account)
	}
	if status.AppleLogin.CreateChannel != AppleChannelAccount {
		t.Fatalf("legacy healthy account was not selected: %#v", status.AppleLogin)
	}
}

func TestExpiredAppleAccountCreateRefreshesBeforeUsingWebFallback(t *testing.T) {
	tokenRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/account/manage/gs/ws/token":
			tokenRequests++
			w.Header().Set("scnt", "fresh-scnt")
			_, _ = w.Write([]byte(`{"timeOutInterval":15}`))
		case "/account/manage":
			_, _ = w.Write([]byte(`{"apiKey":"fresh-api-key"}`))
		case "/account/manage/forwardemail":
			_, _ = w.Write([]byte(`{"forwardToEmail":"owner@example.com"}`))
		case "/v2/jslogs":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/account/manage/email/private/add":
			_, _ = w.Write([]byte(`{"emailAddress":"created@icloud.com"}`))
		case "/account/manage/email/private/add/complete":
			_, _ = w.Write([]byte(`{"id":"created-id","emailAddress":"created@icloud.com","label":"shopping","active":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	state := healthyAppleAccountState()
	state.HealthState = AppleAccountStateHealthy
	state.ManageExpiresAt = time.Now().Add(-time.Minute)
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(server)
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	alias, err := manager.CreateAlias(context.Background(), "shopping", "")
	if err != nil || alias["usedChannel"] != AppleChannelAccount || tokenRequests == 0 {
		t.Fatalf("expired Apple state did not refresh before create: alias=%#v requests=%d err=%v", alias, tokenRequests, err)
	}
}

func TestListAliasesFallsBackToWebAfterAppleTransientFailure(t *testing.T) {
	appleRequests := 0
	appleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appleRequests++
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"reason":"temporary upstream failure"}`))
	}))
	defer appleServer.Close()
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[{"hme":"web-fallback@icloud.com"}]}}`))
	}))
	defer webServer.Close()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	manager.appleAuth.httpClient = appleTestHTTPClient(appleServer)
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(webServer.URL), WithHTTPClient(webServer.Client()))
	}
	if err := storage.WriteJSON(manager.appleAccountPath, healthyAppleAccountState(), 0o600); err != nil {
		t.Fatal(err)
	}
	aliases, source, err := manager.listAliases(context.Background())
	if err != nil || source != AppleChannelICloudWeb || len(aliases) != 1 || aliases[0]["hme"] != "web-fallback@icloud.com" {
		t.Fatalf("transient Apple list did not fall back to Web: source=%s aliases=%#v err=%v", source, aliases, err)
	}
	if appleRequests != appleRequestMaxAttempts {
		t.Fatalf("unexpected Apple retry count: %d", appleRequests)
	}
	var stored AppleAccountState
	if err := storage.ReadJSON(manager.appleAccountPath, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.HealthState != AppleAccountStateDegraded || !stored.LastCheckOK {
		t.Fatalf("transient list failure was not persisted as degraded: %#v", stored)
	}
}

func TestAppleAccountMismatchKeepAliveReturnsError(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	config := testConfig()
	config.AppleID = "different@example.com"
	if err := storage.WriteJSON(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSessionManager(configPath, filepath.Join(root, "state"))
	state := healthyAppleAccountState()
	if err := storage.WriteJSON(manager.appleAccountPath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	err := manager.KeepAliveAppleAccount(context.Background())
	if err == nil {
		t.Fatal("account mismatch was reported as a successful keepalive")
	}
	var stored AppleAccountState
	if err := storage.ReadJSON(manager.appleAccountPath, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.requiresReauth() {
		t.Fatalf("account mismatch did not persist reauth state: %#v", stored)
	}
}
