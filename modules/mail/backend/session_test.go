package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
