package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteJSONRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	input := map[string]any{"enabled": true, "interval": float64(600)}
	if err := WriteJSON(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	input["interval"] = float64(900)
	if err := WriteJSON(path, input, 0o600); err != nil {
		t.Fatal(err)
	}
	output := map[string]any{}
	if err := ReadJSON(path, &output); err != nil {
		t.Fatal(err)
	}
	if output["enabled"] != true || output["interval"] != float64(900) {
		t.Fatalf("unexpected state: %#v", output)
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("state file missing: %v", err)
	}
}

func TestPostgresStateDoesNotCaptureSystemUpdateFiles(t *testing.T) {
	// The routing decision is exercised indirectly by production configuration;
	// this test protects the explicit system/ exclusion from future refactors.
	if stateDB(filepath.Join("D:", "runtime", "system", "update-status.json")) != nil {
		t.Fatal("system update files must remain file-backed")
	}
}
