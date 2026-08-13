package mail

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestCreateSchedulerUsesSafeDefaultsAndClampsUpdates(t *testing.T) {
	root := t.TempDir()
	service := NewCreateScheduler(root, NewSessionManager(filepath.Join(root, "config.json"), root))
	status := service.Status()
	if status.Enabled || status.BatchSize != 5 || status.AliasIntervalSeconds != 3 || status.IntervalSeconds != 180 || status.Label != "shopping" {
		t.Fatalf("unexpected defaults: %#v", status)
	}
	enabled := true
	batch, aliasInterval, interval := 100, 0, 1
	label := "  "
	status, err := service.Update(&enabled, &batch, &aliasInterval, &interval, &label, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Enabled || status.BatchSize != 20 || status.AliasIntervalSeconds != 1 || status.IntervalSeconds != 60 || status.Label != "shopping" || status.RemainingSeconds == nil {
		t.Fatalf("updates were not normalized: %#v", status)
	}
}

func TestCreateSchedulerRunsOneBatchAndPersistsProgress(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "hme-config.json")
	if err := storage.WriteJSON(configPath, testConfig(), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/hme/generate" {
			_, _ = w.Write([]byte(`{"success":true,"result":{"hme":"new@icloud.com"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"hme":{"hme":"new@icloud.com","anonymousId":"id"}}}`))
	}))
	defer upstream.Close()
	session := NewSessionManager(configPath, filepath.Join(root, "state"))
	session.newClient = func(config ICloudConfig) (*Client, error) {
		return NewClient(config, WithBaseURL(upstream.URL), WithHTTPClient(upstream.Client()))
	}
	service := NewCreateScheduler(filepath.Join(root, "state"), session)
	batch, aliasInterval := 2, 1
	if _, err := service.Update(nil, &batch, &aliasInterval, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.RunNow(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for service.Status().Running && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	status := service.Status()
	if status.Running || status.LastBatchSuccess != 2 || requests != 4 || status.LastBatchStoppedReason == nil || *status.LastBatchStoppedReason != "本轮创建完成" {
		t.Fatalf("unexpected completed status: %#v requests=%d", status, requests)
	}
	if err := service.RunNow(); err != nil {
		t.Fatal(err)
	}
	if err := service.RunNow(); err != ErrCreateInProgress {
		t.Fatalf("expected duplicate run to be rejected, got %v", err)
	}
	deadline = time.Now().Add(4 * time.Second)
	for service.Status().Running && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
}

func TestCreateSchedulerDoesNotRunDisabledPeriodicTask(t *testing.T) {
	root := t.TempDir()
	service := NewCreateScheduler(root, NewSessionManager(filepath.Join(root, "config.json"), root))
	service.Start()
	defer service.Shutdown()
	service.runIfDue()
	if service.Status().Running {
		t.Fatal("disabled scheduler started a batch")
	}
}
