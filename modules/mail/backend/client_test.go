package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testConfig() ICloudConfig {
	return ICloudConfig{Host: "p120-maildomainws.icloud.com", DSID: "d", ClientID: "c", ClientBuildNumber: "b", ClientMasteringNumber: "m", Cookie: "SESSION=secret"}
}

func TestClientCreatesAliasWithGenerateAndReserve(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Cookie") != "SESSION=secret" {
			t.Error("session cookie missing")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/hme/generate":
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":"new@icloud.com"}}`))
		case "/v1/hme/reserve":
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["label"] != "shopping" || payload["note"] != "" {
				t.Errorf("unexpected reserve payload: %#v", payload)
			}
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":{"hme":"new@icloud.com","anonymousId":"id-1"}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewClient(testConfig(), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateAlias(context.Background(), "shopping", "")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || created["anonymousId"] != "id-1" {
		t.Fatalf("unexpected create result: %#v", created)
	}
}

func TestClientSurfacesAppleLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"error":{"errorCode":"-41015","errorMessage":"limit"}}`))
	}))
	defer server.Close()
	client, _ := NewClient(testConfig(), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	_, err := client.GenerateAlias(context.Background())
	if err == nil || !stringsContains(err.Error(), "-41015") {
		t.Fatalf("expected Apple limit error, got %v", err)
	}
}

func TestClientUsesDeactivateAndReactivateEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		active   bool
		endpoint string
	}{
		{name: "deactivate", active: false, endpoint: "/v1/hme/deactivate"},
		{name: "reactivate", active: true, endpoint: "/v1/hme/reactivate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.endpoint {
					t.Fatalf("unexpected endpoint: %s", r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["anonymousId"] != "id-1" {
					t.Fatalf("unexpected payload: %#v", payload)
				}
				_, _ = w.Write([]byte(`{"success":true,"result":{"message":"success"}}`))
			}))
			defer server.Close()

			client, err := NewClient(testConfig(), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.SetAliasActive(context.Background(), "id-1", test.active); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestClientExplainsEmptyUpstreamErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	client, _ := NewClient(testConfig(), WithBaseURL(server.URL), WithHTTPClient(server.Client()))

	_, err := client.SetAliasActive(context.Background(), "id-1", true)
	if err == nil || !strings.Contains(err.Error(), "上游未返回正文") {
		t.Fatalf("expected useful empty-body error, got %v", err)
	}
}

func TestClientRollsResponseCookiesIntoFollowingRequests(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "renewed", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "FRESH", Value: "value", Path: "/"})
		} else if cookie := r.Header.Get("Cookie"); !strings.Contains(cookie, "SESSION=renewed") || !strings.Contains(cookie, "FRESH=value") {
			t.Fatalf("rolled cookies were not sent: %q", cookie)
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"hmeEmails":[]}}`))
	}))
	defer server.Close()
	client, err := NewClient(testConfig(), WithBaseURL(server.URL), WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListAliases(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListAliases(context.Background()); err != nil {
		t.Fatal(err)
	}
	config, changed := client.ConfigUpdate()
	if !changed || !strings.Contains(config.Cookie, "SESSION=renewed") || strings.Contains(config.Cookie, "SESSION=secret") {
		t.Fatalf("unexpected rolled config: changed=%v cookie=%q", changed, config.Cookie)
	}
}

func stringsContains(value, needle string) bool {
	for index := 0; index+len(needle) <= len(value); index++ {
		if value[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}
