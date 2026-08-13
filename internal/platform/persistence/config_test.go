package persistence

import "testing"

func TestLoadConfigDefaultsToJSON(t *testing.T) {
	t.Setenv("RUNNING_STORAGE_MODE", "")
	t.Setenv("RUNNING_DATABASE_URL", "")
	t.Setenv("RUNNING_REDIS_ADDR", "")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != StorageJSON || cfg.DatabaseURL != "" || cfg.RedisPrefix != "running-tools:" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestLoadConfigRequiresDatabaseOutsideJSONMode(t *testing.T) {
	t.Setenv("RUNNING_STORAGE_MODE", "dual")
	t.Setenv("RUNNING_DATABASE_URL", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("dual mode accepted without PostgreSQL URL")
	}
}

func TestLoadConfigNormalizesRedisPrefix(t *testing.T) {
	t.Setenv("RUNNING_STORAGE_MODE", "postgres")
	t.Setenv("RUNNING_DATABASE_URL", "postgres://example")
	t.Setenv("RUNNING_REDIS_PREFIX", "custom")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RedisPrefix != "custom:" {
		t.Fatalf("prefix was not normalized: %q", cfg.RedisPrefix)
	}
}

func TestLoadConfigBuildsEncodedPostgresURL(t *testing.T) {
	t.Setenv("RUNNING_STORAGE_MODE", "dual")
	t.Setenv("RUNNING_DATABASE_URL", "")
	t.Setenv("RUNNING_POSTGRES_HOST", "postgres")
	t.Setenv("RUNNING_POSTGRES_PORT", "5432")
	t.Setenv("RUNNING_POSTGRES_USER", "running_tools")
	t.Setenv("RUNNING_POSTGRES_PASSWORD", "colon:@/ password")
	t.Setenv("RUNNING_POSTGRES_DB", "running_tools")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://running_tools:colon%3A%40%2F%20password@postgres:5432/running_tools?sslmode=disable" {
		t.Fatalf("unexpected encoded URL: %q", cfg.DatabaseURL)
	}
}
