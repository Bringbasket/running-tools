package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/activitylog"
	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

func TestAccountHeaderSelectsIsolatedRuntime(t *testing.T) {
	root := t.TempDir()
	module := NewModule(root, "", "")
	second, err := module.createAccount("第二账号")
	if err != nil {
		t.Fatal(err)
	}
	defaultRuntime, _ := module.runtime(defaultMailAccountID)
	secondRuntime, _ := module.runtime(second.ID)
	if defaultRuntime.session.configPath == secondRuntime.session.configPath || secondRuntime.session.configPath != filepath.Join(root, "accounts", second.ID, "hme-config.json") {
		t.Fatalf("account state paths are not isolated: %q %q", defaultRuntime.session.configPath, secondRuntime.session.configPath)
	}
	mux := http.NewServeMux()
	module.RegisterRoutes(mux, httpx.APIKey("secret"))
	request := httptest.NewRequest(http.MethodGet, "/api/mail/v1/session/status", nil)
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("X-Mail-Account-ID", second.ID)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestUnknownAccountIsRejected(t *testing.T) {
	module := NewModule(t.TempDir(), "", "")
	mux := http.NewServeMux()
	module.RegisterRoutes(mux, httpx.APIKey("secret"))
	request := httptest.NewRequest(http.MethodGet, "/api/mail/v1/session/status", nil)
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("X-Mail-Account-ID", "missing")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestDeleteAccountStopsRuntimeAndRemovesLocalState(t *testing.T) {
	root := t.TempDir()
	module := NewModule(root, "", "")
	account, err := module.createAccount("待删除账号")
	if err != nil {
		t.Fatal(err)
	}
	accountRoot := filepath.Join(root, "accounts", account.ID)
	if err := os.MkdirAll(filepath.Join(accountRoot, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accountRoot, "state", "test.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	module.Start()
	t.Cleanup(module.Stop)

	if err := module.deleteAccount(context.Background(), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := module.runtime(account.ID); ok {
		t.Fatal("deleted account runtime is still registered")
	}
	if accounts := module.accountList(); len(accounts) != 1 || accounts[0].ID != defaultMailAccountID {
		t.Fatalf("unexpected account list after delete: %#v", accounts)
	}
	if _, err := os.Stat(accountRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("account state directory was not removed: %v", err)
	}
}

func TestDefaultAccountCannotBeDeleted(t *testing.T) {
	module := NewModule(t.TempDir(), "", "")
	if err := module.deleteAccount(context.Background(), defaultMailAccountID); !errors.Is(err, errDefaultAccountProtected) {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := module.runtime(defaultMailAccountID); !ok {
		t.Fatal("default account runtime was removed")
	}
}

func TestDeleteAccountAPI(t *testing.T) {
	module := NewModule(t.TempDir(), "", "")
	account, err := module.createAccount("接口删除账号")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	module.RegisterRoutes(mux, httpx.APIKey("secret"))

	request := httptest.NewRequest(http.MethodDelete, "/api/mail/v1/accounts/"+account.ID, nil)
	request.Header.Set("X-API-Key", "secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected delete response: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/mail/v1/accounts/default", nil)
	request.Header.Set("X-API-Key", "secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("unexpected protected response: %d %s", response.Code, response.Body.String())
	}
}

func TestAccountProxyAPIStoresButNeverReturnsCredentials(t *testing.T) {
	module := NewModule(t.TempDir(), "", "")
	mux := http.NewServeMux()
	module.RegisterRoutes(mux, httpx.APIKey("secret"))
	proxyURL := "http://proxy-user:proxy-password@127.0.0.1:8080"
	body, _ := json.Marshal(map[string]string{"proxy": proxyURL})
	request := httptest.NewRequest(http.MethodPut, "/api/mail/v1/accounts/default/proxy", bytes.NewReader(body))
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "proxy-user") || !strings.Contains(response.Body.String(), `"hasProxy":true`) {
		t.Fatalf("unexpected proxy response: %d %s", response.Code, response.Body.String())
	}
	runtime, _ := module.runtime(defaultMailAccountID)
	if runtime.account.ProxyURL != proxyURL || runtime.session.proxyURL != proxyURL {
		t.Fatalf("proxy was not applied to the account runtime")
	}

	request = httptest.NewRequest(http.MethodGet, "/api/mail/v1/accounts", nil)
	request.Header.Set("X-API-Key", "secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if strings.Contains(response.Body.String(), "proxy-password") || !strings.Contains(response.Body.String(), `"hasProxy":true`) {
		t.Fatalf("account list leaked proxy credentials: %s", response.Body.String())
	}
}

func TestAccountProxyTestDoesNotPersistOrLeakCredentials(t *testing.T) {
	proxyRequests := 0
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyRequests++
		if r.URL.Host != "www.icloud.test" {
			t.Fatalf("unexpected proxy target: %s", r.URL.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxyServer.Close()

	module := NewModule(t.TempDir(), "", "")
	runtime, _ := module.runtime(defaultMailAccountID)
	beforeAccountProxy := runtime.account.ProxyURL
	beforeSessionProxy := runtime.session.proxyURL
	proxyURL := "http://proxy-user:proxy-password@" + strings.TrimPrefix(proxyServer.URL, "http://")
	api := &routeAPI{module: module, proxyTestTarget: "http://www.icloud.test/"}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)

	body, _ := json.Marshal(map[string]string{"proxy": proxyURL})
	request := httptest.NewRequest(http.MethodPost, "/api/mail/v1/accounts/default/proxy/test", bytes.NewReader(body))
	request.Header.Set("X-API-Key", "secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)

	if response.Code != http.StatusOK || proxyRequests != 1 || !strings.Contains(response.Body.String(), `"statusCode":204`) {
		t.Fatalf("unexpected proxy test response: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "proxy-user") || strings.Contains(response.Body.String(), "proxy-password") {
		t.Fatalf("proxy test response leaked credentials: %s", response.Body.String())
	}
	if runtime.account.ProxyURL != beforeAccountProxy || runtime.session.proxyURL != beforeSessionProxy {
		t.Fatalf("proxy test changed runtime configuration: account=%q session=%q", runtime.account.ProxyURL, runtime.session.proxyURL)
	}
	if module.accountList()[0].HasProxy {
		t.Fatal("proxy test marked the account as configured")
	}
}

func TestAccountProxyTestValidatesAccountAndProxy(t *testing.T) {
	module := NewModule(t.TempDir(), "", "")
	api := &routeAPI{module: module, proxyTestTarget: "http://www.icloud.test/"}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)

	tests := []struct {
		name   string
		id     string
		proxy  string
		status int
	}{
		{name: "unknown account", id: "missing", proxy: "http://127.0.0.1:8080", status: http.StatusNotFound},
		{name: "invalid proxy", id: "default", proxy: "ftp://proxy-user:proxy-password@127.0.0.1:21", status: http.StatusBadRequest},
		{name: "empty proxy", id: "default", proxy: "", status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"proxy": test.proxy})
			request := httptest.NewRequest(http.MethodPost, "/api/mail/v1/accounts/"+test.id+"/proxy/test", bytes.NewReader(body))
			request.Header.Set("X-API-Key", "secret")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "proxy-user") || strings.Contains(response.Body.String(), "proxy-password") {
				t.Fatalf("error response leaked credentials: %s", response.Body.String())
			}
		})
	}
}

func TestAccountManagementRequestsAreLoggedWithoutSensitiveData(t *testing.T) {
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "www.icloud.test" {
			t.Errorf("unexpected proxy target: %s", r.URL.String())
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxyServer.Close()

	module := NewModule(t.TempDir(), "", "")
	api := &routeAPI{module: module, proxyTestTarget: "http://www.icloud.test/"}
	mux := http.NewServeMux()
	api.register(mux, httpx.APIKey("secret"), "/api/mail/v1", false)
	proxyURL := "http://proxy-user:proxy-password@" + strings.TrimPrefix(proxyServer.URL, "http://")

	doRequest := func(method, path string, payload any) *httptest.ResponseRecorder {
		var body *bytes.Reader
		if payload == nil {
			body = bytes.NewReader(nil)
		} else {
			encoded, _ := json.Marshal(payload)
			body = bytes.NewReader(encoded)
		}
		request := httptest.NewRequest(method, path, body)
		request.Header.Set("X-API-Key", "secret")
		request.Header.Set("X-Mail-Account-ID", defaultMailAccountID)
		request.Header.Set("Content-Type", "application/json")
		request = request.WithContext(contextWithRequestID(request.Context(), "account-log-request"))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	if response := doRequest(http.MethodPost, "/api/mail/v1/accounts", map[string]string{"name": "日志母号"}); response.Code != http.StatusCreated {
		t.Fatalf("unexpected create response: %d %s", response.Code, response.Body.String())
	}
	createdID := ""
	for _, account := range module.accountList() {
		if account.Name == "日志母号" {
			createdID = account.ID
			break
		}
	}
	if createdID == "" {
		t.Fatal("created account was not found")
	}
	if response := doRequest(http.MethodPost, "/api/mail/v1/accounts/default/proxy/test", map[string]string{"proxy": proxyURL}); response.Code != http.StatusOK {
		t.Fatalf("unexpected proxy test response: %d %s", response.Code, response.Body.String())
	}
	if response := doRequest(http.MethodPut, "/api/mail/v1/accounts/default/proxy", map[string]string{"proxy": proxyURL}); response.Code != http.StatusOK {
		t.Fatalf("unexpected proxy save response: %d %s", response.Code, response.Body.String())
	}
	if response := doRequest(http.MethodPost, "/api/mail/v1/accounts/default/proxy/test", map[string]string{"proxy": "ftp://proxy-user:proxy-password@127.0.0.1:21"}); response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected failed proxy test response: %d %s", response.Code, response.Body.String())
	}
	if response := doRequest(http.MethodDelete, "/api/mail/v1/accounts/"+createdID, nil); response.Code != http.StatusOK {
		t.Fatalf("unexpected delete response: %d %s", response.Code, response.Body.String())
	}

	runtime, _ := module.runtime(defaultMailAccountID)
	page, err := runtime.logs.Query(context.Background(), activitylog.Query{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 5 {
		t.Fatalf("unexpected account log count: %d %#v", page.Total, page.Items)
	}
	actions := map[string]bool{}
	for _, entry := range page.Items {
		actions[entry.Action] = true
		if entry.RequestID != "account-log-request" {
			t.Fatalf("request id was not recorded: %#v", entry)
		}
		encoded, _ := json.Marshal(entry)
		if strings.Contains(string(encoded), "proxy-user") || strings.Contains(string(encoded), "proxy-password") {
			t.Fatalf("proxy credentials leaked into activity log: %s", encoded)
		}
	}
	for _, action := range []string{"account.create", "account.proxy.test", "account.proxy.update", "account.delete"} {
		if !actions[action] {
			t.Fatalf("missing account activity action %q: %#v", action, actions)
		}
	}
	var failed bool
	for _, entry := range page.Items {
		if entry.Action == "account.proxy.test" && entry.Outcome == "failure" && entry.HTTPStatus == http.StatusBadRequest {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("failed proxy test was not logged: %#v", page.Items)
	}
}

func TestAccountHealthIgnoresDisabledIMAPError(t *testing.T) {
	checkedAt := unixNow()
	summary := MailAccountSummary{
		ICloudWeb: accountChannelHealth{Configured: true, Healthy: true, LastCheckedAt: &checkedAt},
		Mailbox:   accountMailboxHealth{Configured: true, Enabled: false, LastError: "old failure"},
	}
	status, message := accountHealthStatus(true, summary, AutoRefreshConfig{}, CreateScheduleConfig{}, AliasQueueStatus{})
	if status != "active" || message != "运行正常" {
		t.Fatalf("disabled IMAP error affected health: %s %s", status, message)
	}
}

func TestAccountHealthIncludesEnabledAutomationErrors(t *testing.T) {
	checkedAt := unixNow()
	failure := "background failure"
	summary := MailAccountSummary{ICloudWeb: accountChannelHealth{Configured: true, Healthy: true, LastCheckedAt: &checkedAt}}
	tests := []struct {
		name     string
		refresh  AutoRefreshConfig
		creation CreateScheduleConfig
		message  string
	}{
		{name: "auto refresh", refresh: AutoRefreshConfig{Enabled: true, LastError: &failure}, message: "Session 自动检测异常"},
		{name: "auto create", creation: CreateScheduleConfig{Enabled: true, LastError: &failure}, message: "自动创建任务异常"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, message := accountHealthStatus(true, summary, test.refresh, test.creation, AliasQueueStatus{})
			if status != "warning" || message != test.message {
				t.Fatalf("automation error was not summarized: %s %s", status, message)
			}
		})
	}
}
