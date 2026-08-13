package mail

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Bringbasket/running-tools/internal/platform/httpx"
)

func TestAccountHeaderSelectsIsolatedRuntime(t *testing.T) {
	root := t.TempDir()
	module := NewModule(root, "", "")
	second, err := module.createAccount("第二账号")
	if err != nil { t.Fatal(err) }
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
	if response.Code != http.StatusOK { t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String()) }
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
	if response.Code != http.StatusNotFound { t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String()) }
}
