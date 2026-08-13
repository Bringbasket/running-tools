package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Address       string
	APIKey        string
	DataDir       string
	Version       string
	RepositoryURL string
}

func Load() (Config, error) {
	dataDir := envOr("RUNNING_DATA_DIR", "data")
	cfg := Config{
		Address:       envOr("RUNNING_ADDR", ":8000"),
		APIKey:        strings.TrimSpace(os.Getenv("RUNNING_API_KEY")),
		DataDir:       filepath.Clean(dataDir),
		Version:       envOr("RUNNING_REVISION", envOr("RUNNING_VERSION", "0.0.1")),
		RepositoryURL: envOr("RUNNING_REPOSITORY_URL", "https://github.com/Bringbasket/running-tools"),
	}
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("RUNNING_API_KEY is required")
	}
	return cfg, nil
}

func envOr(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}
