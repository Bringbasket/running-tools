package mail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestAliasTimestampFromMapSupportsAppleVariants(t *testing.T) {
	want := float64(time.Date(2026, time.August, 13, 10, 0, 0, 0, time.UTC).UnixMilli())
	tests := []struct {
		name  string
		alias map[string]any
	}{
		{name: "milliseconds", alias: map[string]any{"createTimestamp": want}},
		{name: "seconds", alias: map[string]any{"createdTimestamp": want / 1000}},
		{name: "numeric string", alias: map[string]any{"createdAt": "1786615200000"}},
		{name: "rfc3339", alias: map[string]any{"createdDate": "2026-08-13T10:00:00Z"}},
		{name: "creation date", alias: map[string]any{"creationDate": "2026-08-13 10:00:00"}},
		{name: "date created", alias: map[string]any{"dateCreated": "2026-08-13T10:00:00.000Z"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := aliasTimestampFromMap(test.alias)
			if !ok || got != want {
				t.Fatalf("timestamp = %v, %v; want %v, true", got, ok, want)
			}
		})
	}
}

func TestNormalizeAliasTimestampRejectsInvalidValues(t *testing.T) {
	alias := map[string]any{"createTimestamp": float64(0), "createdDate": "not-a-date"}
	normalizeAliasTimestamp(alias)
	if _, ok := aliasTimestamp(alias["createTimestamp"]); ok {
		t.Fatalf("invalid timestamp was accepted: %#v", alias)
	}
}

func TestEnrichAliasTimestampsUsesWebFallbackAndCache(t *testing.T) {
	const createdAt = float64(1786615200000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/hme/list" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[{"anonymousId":"web-id","hme":"same@icloud.com","createdDate":"2026-08-13T10:00:00Z"}]}}`))
	}))
	defer server.Close()

	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	manager.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	}
	if err := storage.WriteJSON(manager.configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}

	aliases := []map[string]any{{"anonymousId": "account-id", "hme": "same@icloud.com"}}
	manager.enrichAliasTimestamps(context.Background(), aliases)
	if got, ok := aliasTimestamp(aliases[0]["createTimestamp"]); !ok || got != createdAt {
		t.Fatalf("web fallback timestamp = %v, %v; want %v, true", got, ok, createdAt)
	}

	manager.newClient = func(ICloudConfig) (*Client, error) { return nil, errors.New("web unavailable") }
	cachedAliases := []map[string]any{{"anonymousId": "another-id", "hme": "SAME@ICLOUD.COM"}}
	manager.enrichAliasTimestamps(context.Background(), cachedAliases)
	if got, ok := aliasTimestamp(cachedAliases[0]["createTimestamp"]); !ok || got != createdAt {
		t.Fatalf("cached timestamp = %v, %v; want %v, true", got, ok, createdAt)
	}
}

func TestRememberAliasTimestampPersistsForLaterLists(t *testing.T) {
	root := t.TempDir()
	manager := NewSessionManager(filepath.Join(root, "config.json"), filepath.Join(root, "state"))
	want := float64(time.Now().Truncate(time.Second).UnixMilli())
	manager.rememberAliasTimestamp(map[string]any{
		"anonymousId":     "created-id",
		"hme":             "created@icloud.com",
		"createTimestamp": want,
	})

	aliases := []map[string]any{{"hme": "CREATED@ICLOUD.COM"}}
	manager.enrichAliasTimestamps(context.Background(), aliases)
	if got, ok := aliasTimestamp(aliases[0]["createTimestamp"]); !ok || got != want {
		t.Fatalf("remembered timestamp = %v, %v; want %v, true", got, ok, want)
	}
}

func TestRememberAliasTimestampIgnoresAliasesWithoutIdentity(t *testing.T) {
	manager := NewSessionManager(filepath.Join(t.TempDir(), "config.json"), t.TempDir())
	manager.rememberAliasTimestamp(map[string]any{"createTimestamp": float64(time.Now().UnixMilli())})
	if _, err := os.Stat(manager.aliasTimestampPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timestamp cache should not be created, stat error: %v", err)
	}
}
