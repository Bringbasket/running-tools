package mail

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Bringbasket/running-tools/internal/platform/storage"
)

func TestAliasQueuePersistsAndControlsJob(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "hme-config.json")
	if err := writeTestConfig(configPath); err != nil {
		t.Fatal(err)
	}
	queue := NewAliasQueue(filepath.Join(root, "state"), NewSessionManager(configPath, filepath.Join(root, "state")))
	status, err := queue.Enqueue(nil, "shopping", 3, "", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "queued" || status.Requested != 3 {
		t.Fatalf("unexpected queue status: %#v", status)
	}
	if status.AccountDSID != "" {
		t.Fatal("account DSID leaked in public status")
	}
	var persisted aliasQueuePersisted
	if err := storage.ReadJSON(queue.path, &persisted); err != nil || persisted.Job == nil || persisted.Job.AccountDSID != "d" {
		t.Fatalf("account binding not persisted: %#v %v", persisted, err)
	}
	repeated, err := queue.Enqueue(nil, "shopping", 3, "", "request-1")
	if err != nil || repeated.JobID != status.JobID {
		t.Fatalf("idempotent enqueue failed: %#v %v", repeated, err)
	}
	paused, err := queue.Pause(status.JobID)
	if err != nil || paused.Status != "paused" {
		t.Fatalf("pause failed: %#v %v", paused, err)
	}
	resumed, err := queue.Resume(status.JobID, false)
	if err != nil || resumed.Status != "queued" {
		t.Fatalf("resume failed: %#v %v", resumed, err)
	}
	reloaded := NewAliasQueue(filepath.Join(root, "state"), queue.session)
	if got := reloaded.Status(); got.JobID != status.JobID || got.Requested != 3 {
		t.Fatalf("queue was not persisted: %#v", got)
	}
	cancelled, err := reloaded.Cancel(status.JobID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel failed: %#v %v", cancelled, err)
	}
}

func TestAliasQueueUsesAdaptiveWakeDelay(t *testing.T) {
	root := t.TempDir()
	queue := NewAliasQueue(root, NewSessionManager(filepath.Join(root, "config.json"), root))
	if delay := queue.nextWakeDelay(); delay != aliasQueueIdlePoll {
		t.Fatalf("idle delay = %s, want %s", delay, aliasQueueIdlePoll)
	}
	next := unixNow() + 2
	if err := storage.WriteJSON(queue.path, aliasQueuePersisted{Job: &AliasQueueStatus{
		JobID: "one", Status: "waiting_retry", NextAttemptAt: &next,
	}}, 0o600); err != nil {
		t.Fatal(err)
	}
	if delay := queue.nextWakeDelay(); delay < time.Second || delay > 2*time.Second {
		t.Fatalf("scheduled delay = %s, want approximately 2s", delay)
	}
}

func TestAliasQueueRecoversInterruptedStates(t *testing.T) {
	root := t.TempDir()
	session := NewSessionManager(filepath.Join(root, "config.json"), root)
	queue := NewAliasQueue(root, session)
	now := unixNow()
	if err := storage.WriteJSON(queue.path, aliasQueuePersisted{Job: &AliasQueueStatus{JobID: "one", Requested: 2, Status: "running", CandidateHME: "candidate@icloud.com", CandidateState: "reserving", NextAttemptAt: &now}}, 0o600); err != nil {
		t.Fatal(err)
	}
	queue.Start()
	queue.Stop()
	status := queue.Status()
	if status.Status != "needs_attention" || status.LastErrorCode != "RESULT_CONFIRMATION_REQUIRED" {
		t.Fatalf("unsafe recovery: %#v", status)
	}
}

func writeTestConfig(path string) error {
	return storage.WriteJSON(path, testConfig(), 0o600)
}
