package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFileKeepsExistingEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("RUNNING_TEST_ONE=file\nRUNNING_TEST_TWO='quoted value'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNNING_TEST_ONE", "process")
	os.Unsetenv("RUNNING_TEST_TWO")
	t.Cleanup(func() { _ = os.Unsetenv("RUNNING_TEST_TWO") })
	if err := LoadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	if value := os.Getenv("RUNNING_TEST_ONE"); value != "process" {
		t.Fatalf("existing environment was overwritten: %q", value)
	}
	if value := os.Getenv("RUNNING_TEST_TWO"); value != "quoted value" {
		t.Fatalf("dotenv value not loaded: %q", value)
	}
}
