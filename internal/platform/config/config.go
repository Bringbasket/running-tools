package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Address        string
	AdminUsername  string
	AuthSessionTTL time.Duration
	TrustProxy     bool
	DataDir        string
	Version        string
	Revision       string
	RepositoryURL  string
}

func Load() (Config, error) {
	dataDir := envOr("RUNNING_DATA_DIR", "data")
	cfg := Config{
		Address:        envOr("RUNNING_ADDR", ":8000"),
		AdminUsername:  strings.ToLower(envOr("RUNNING_ADMIN_USERNAME", "admin")),
		AuthSessionTTL: time.Duration(envInt("RUNNING_AUTH_SESSION_HOURS", 168)) * time.Hour,
		TrustProxy:     envBool("RUNNING_TRUST_PROXY", false),
		DataDir:        filepath.Clean(dataDir),
		Version:        envOr("RUNNING_VERSION", "0.0.1"),
		Revision:       envOr("RUNNING_REVISION", "dev"),
		RepositoryURL:  envOr("RUNNING_REPOSITORY_URL", "https://github.com/Bringbasket/hme-tools"),
	}
	if cfg.AdminUsername == "" || len(cfg.AdminUsername) > 64 {
		return Config{}, fmt.Errorf("RUNNING_ADMIN_USERNAME is invalid")
	}
	if cfg.AuthSessionTTL < time.Hour || cfg.AuthSessionTTL > 30*24*time.Hour {
		return Config{}, fmt.Errorf("RUNNING_AUTH_SESSION_HOURS must be between 1 and 720")
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

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
