package config

import "testing"

func TestLoadSeparatesVersionAndRevision(t *testing.T) {
	t.Setenv("RUNNING_VERSION", "0.0.42")
	t.Setenv("RUNNING_REVISION", "abcdef123456")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != "0.0.42" || cfg.Revision != "abcdef123456" {
		t.Fatalf("build metadata = version %q revision %q", cfg.Version, cfg.Revision)
	}
}
