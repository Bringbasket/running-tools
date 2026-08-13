package systemupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCheckQueuesCheckOnlyRequest(t *testing.T) {
	service := newTestService(t)

	status, err := service.Check()
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if status.State != "check_queued" || status.Action != "check" {
		t.Fatalf("Check() status = %#v", status)
	}
	request := readRequest(t, filepath.Join(service.stateDir, "check-request.json"))
	if request.Action != "check" {
		t.Fatalf("request action = %q, want check", request.Action)
	}
	if _, err := os.Stat(filepath.Join(service.stateDir, "update-request.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("update request should not exist, stat error = %v", err)
	}
}

func TestUpdateRequiresCompletedCheck(t *testing.T) {
	service := newTestService(t)

	if _, err := service.Request(); !errors.Is(err, ErrCheckRequired) {
		t.Fatalf("Request() error = %v, want ErrCheckRequired", err)
	}
}

func TestUpdateQueuesAfterNewRevisionWasFound(t *testing.T) {
	service := newTestService(t)
	latest := "revision-b"
	writeStatus(t, filepath.Join(service.stateDir, "update-status.json"), Status{
		State:           "update_available",
		LatestRevision:  &latest,
		UpdateAvailable: boolPointer(true),
	})

	status, err := service.Request()
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if status.State != "update_queued" || status.Action != "update" {
		t.Fatalf("Request() status = %#v", status)
	}
	request := readRequest(t, filepath.Join(service.stateDir, "update-request.json"))
	if request.Action != "update" {
		t.Fatalf("request action = %q, want update", request.Action)
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	service := New(t.TempDir(), "revision-a", "https://example.com/repository")
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	return service
}

func readRequest(t *testing.T, path string) requestFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	request := requestFile{}
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return request
}

func writeStatus(t *testing.T, path string, status Status) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create status directory: %v", err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write status: %v", err)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
